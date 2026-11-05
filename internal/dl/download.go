// Package dl downloads assets from GitHub and Hugging Face, transparently
// falling back to public mirrors when direct access is slow or blocked.
package dl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	Progress func(done, total int64)
	// Timeout bounds a stalled attempt before the next mirror is tried.
	Timeout time.Duration
}

var client = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	MaxIdleConnsPerHost:   4,
	ResponseHeaderTimeout: 15 * time.Second,
	DisableCompression:    true,
}}

// To downloads rawURL into dst atomically. Mirrors are tried in order.
func To(ctx context.Context, rawURL, dst string, o Options) (string, error) {
	if o.Timeout == 0 {
		o.Timeout = 20 * time.Second
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
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()

	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "translator233/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: http %d", url, resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
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
				return "", werr
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
			return "", rerr
		}
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	return url, nil
}
