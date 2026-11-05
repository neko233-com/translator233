// Package dl downloads assets from GitHub and Hugging Face, transparently
// falling back to public mirrors when direct access is slow or blocked, and
// splitting large files across several connections.
package dl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// GitHubMirrors are URL prefixes that proxy github.com / raw.githubusercontent.com.
var GitHubMirrors = []string{
	"https://ghfast.top/",
	"https://gh-proxy.com/",
	"https://ghproxy.net/",
	"https://ghp.ci/",
}

// HFMirrors are URL prefixes for huggingface.co.
var HFMirrors = []string{
	"https://hf-mirror.com/",
}

// DefaultConnections is the number of parallel connections for large files.
const DefaultConnections = 8

// chunkSize is the minimum size of one ranged request.
const chunkSize = 4 << 20

// Candidates returns the direct URL followed by mirror-rewritten variants.
func Candidates(rawURL string) []string {
	out := []string{rawURL}
	switch {
	case strings.Contains(rawURL, "://github.com/") ||
		strings.Contains(rawURL, "://raw.githubusercontent.com/") ||
		strings.Contains(rawURL, "://objects.githubusercontent.com/"):
		for _, m := range GitHubMirrors {
			out = append(out, m+rawURL)
		}
	case strings.Contains(rawURL, "://huggingface.co/") || strings.Contains(rawURL, "://cdn-lfs.huggingface.co/"):
		for _, m := range HFMirrors {
			out = append(out, strings.Replace(rawURL, "://huggingface.co/", "://hf-mirror.com/", 1))
			out = append(out, m+rawURL)
		}
	}
	return out
}

// Options controls a download.
type Options struct {
	// Progress is called with bytes written and total size (-1 if unknown).
	// It is called concurrently and may receive out-of-order totals.
	Progress func(done, total int64)
	// Timeout bounds a stalled connection before the next mirror is tried.
	Timeout time.Duration
	// Connections is the number of parallel requests for large files.
	Connections int
	// NoSplit forces a single connection.
	NoSplit bool
}

var client = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	MaxIdleConnsPerHost:   16,
	ResponseHeaderTimeout: 20 * time.Second,
	DisableCompression:    true,
}}

// To downloads rawURL into dst atomically. Mirrors are tried in order.
func To(ctx context.Context, rawURL, dst string, o Options) (string, error) {
	if o.Timeout == 0 {
		o.Timeout = 20 * time.Second
	}
	if o.Connections <= 0 {
		o.Connections = DefaultConnections
	}
	var lastErr error
	for _, candidate := range Candidates(rawURL) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		used, err := attempt(ctx, candidate, dst, o)
		if err == nil {
			return used, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no download source")
	}
	return "", lastErr
}

func attempt(ctx context.Context, url, dst string, o Options) (string, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	size, ranges := probe(ctx, url, o.Timeout)
	if ranges && size > chunkSize && o.Connections > 1 {
		if err := parallel(ctx, url, dst, size, o); err == nil {
			return url, nil
		}
		// Fall through to a single stream if the ranged transfer failed.
	}
	if err := single(ctx, url, dst, o); err != nil {
		return "", err
	}
	return url, nil
}

func probe(ctx context.Context, url string, timeout time.Duration) (int64, bool) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, false
	}
	req.Header.Set("User-Agent", "translator233/1.0")
	req.Header.Set("Range", "bytes=0-0")
	resp, err := client.Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1))
	if resp.StatusCode != http.StatusPartialContent {
		if resp.StatusCode != http.StatusOK {
			return 0, false
		}
		return resp.ContentLength, false
	}
	_, total, ok := parseContentRange(resp.Header.Get("Content-Range"))
	return total, ok
}

// parseContentRange parses "bytes 0-0/12345".
func parseContentRange(v string) (start, total int64, ok bool) {
	if !strings.HasPrefix(v, "bytes ") {
		return 0, 0, false
	}
	rest := strings.TrimPrefix(v, "bytes ")
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return 0, 0, false
	}
	total, err := strconv.ParseInt(rest[slash+1:], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return 0, total, true
}

func newDst(dst string) (*os.File, error) {
	tmp := dst + ".part"
	return os.Create(tmp)
}

func single(ctx context.Context, url, dst string, o Options) error {
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "translator233/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: http %d", url, resp.StatusCode)
	}
	f, err := newDst(dst)
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)

	total := resp.ContentLength
	var done int64
	buf := make([]byte, 256<<10)
	stall := time.AfterFunc(o.Timeout, cancel)
	defer stall.Stop()
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return werr
			}
			done += int64(n)
			stall.Reset(o.Timeout)
			if o.Progress != nil {
				o.Progress(done, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return rerr
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// parallel downloads url with ranged requests written to one file.
func parallel(ctx context.Context, url, dst string, size int64, o Options) error {
	f, err := newDst(dst)
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	defer f.Close()

	conns := o.Connections
	if fewest := int((size + chunkSize - 1) / chunkSize); conns > fewest {
		conns = fewest
	}
	if conns < 2 {
		return errors.New("dl: nothing to split")
	}
	part := (size + int64(conns) - 1) / int64(conns)

	var written atomic.Int64
	var wg sync.WaitGroup
	errCh := make(chan error, conns)
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for i := 0; i < conns; i++ {
		start := int64(i) * part
		end := start + part - 1
		if end >= size {
			end = size - 1
		}
		if start > end {
			continue
		}
		wg.Add(1)
		go func(start, end int64) {
			defer wg.Done()
			if err := fetchRange(sctx, url, f, start, end, size, &written, o); err != nil {
				errCh <- err
				cancel()
			}
		}(start, end)
	}
	wg.Wait()
	select {
	case err := <-errCh:
		return err
	default:
	}
	if written.Load() != size {
		return fmt.Errorf("dl: got %d of %d bytes", written.Load(), size)
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func fetchRange(ctx context.Context, url string, f *os.File, start, end, size int64, written *atomic.Int64, o Options) error {
	var lastErr error
	for try := 0; try < 3; try++ {
		if err := fetchRangeOnce(ctx, url, f, start, end, size, written, o); err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return err
			}
			time.Sleep(time.Duration(try) * 300 * time.Millisecond)
			continue
		}
		return nil
	}
	return lastErr
}

func fetchRangeOnce(ctx context.Context, url string, f *os.File, start, end, size int64, written *atomic.Int64, o Options) error {
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "translator233/1.0")
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("dl: range request got http %d", resp.StatusCode)
	}
	stall := time.AfterFunc(o.Timeout, cancel)
	defer stall.Stop()

	buf := make([]byte, 128<<10)
	off := start
	for off <= end {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if off+int64(n) > end+1 {
				chunk = buf[:end+1-off]
			}
			if _, werr := f.WriteAt(chunk, off); werr != nil {
				return werr
			}
			off += int64(len(chunk))
			total := written.Add(int64(len(chunk)))
			stall.Reset(o.Timeout)
			if o.Progress != nil {
				o.Progress(total, size)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	if off != end+1 {
		return fmt.Errorf("dl: range %d-%d incomplete at %d", start, end, off)
	}
	return nil
}
