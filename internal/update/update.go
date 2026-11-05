// Package update checks the GitHub releases of translator233 and installs a
// newer build when there is one.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/neko233-com/translator233/internal/appdir"
	"github.com/neko233-com/translator233/internal/dl"
)

// Repo is the GitHub repository the updater reads releases from.
const Repo = "neko233-com/translator233"

// Asset is one file of a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Release is a GitHub release.
type Release struct {
	Tag     string  `json:"tag_name"`
	Name    string  `json:"name"`
	HTMLURL string  `json:"html_url"`
	Assets  []Asset `json:"assets"`
	Draft   bool    `json:"draft"`
}

// Version is the release version without the leading v.
func (r *Release) Version() string { return strings.TrimPrefix(r.Tag, "v") }

// Asset finds a file by name.
func (r *Release) Asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// AssetName is the name we publish for a target. kind is "" for the CLI and
// "gui" for the window.
func AssetName(kind, goos, goarch string) string {
	ext := ""
	if goos == "windows" {
		ext = ".exe"
	}
	name := "translator233"
	if kind != "" {
		name += "-" + kind
	}
	return fmt.Sprintf("%s-%s-%s%s", name, goos, goarch, ext)
}

// MSIName is the installer asset for the running architecture.
func MSIName(goos, goarch string) string {
	if goos != "windows" {
		return ""
	}
	return fmt.Sprintf("translator233-windows-%s.msi", goarch)
}

// Latest fetches the newest published release.
func Latest(ctx context.Context) (*Release, error) {
	url := "https://api.github.com/repos/" + Repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "translator233-updater")
	req.Header.Set("Accept", "application/vnd.github+json")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("检查更新失败: http %d", resp.StatusCode)
	}
	var r Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return nil, err
	}
	if r.Draft {
		return nil, fmt.Errorf("最新发布是草稿")
	}
	return &r, nil
}

// Compare returns -1, 0 or 1 comparing dotted versions. A prerelease sorts
// before the release of the same numbers: "1.0.0-beta.1" < "1.0.0".
func Compare(a, b string) int {
	na, preA := splitVersion(a)
	nb, preB := splitVersion(b)
	for i := 0; i < len(na) || i < len(nb); i++ {
		x, y := 0, 0
		if i < len(na) {
			x = na[i]
		}
		if i < len(nb) {
			y = nb[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case preA == preB:
		return 0
	case preA == "":
		return 1
	case preB == "":
		return -1
	case preA < preB:
		return -1
	}
	return 1
}

func splitVersion(v string) ([]int, string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	core, pre, _ := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	nums := make([]int, 0, len(parts))
	for _, p := range parts {
		n, _ := strconv.Atoi(p)
		nums = append(nums, n)
	}
	return nums, pre
}

// Available reports whether the release is newer than current.
func Available(current string, r *Release) bool {
	return r != nil && Compare(r.Version(), current) > 0
}

// Plan is what an update would do on this machine.
type Plan struct {
	Current string
	Latest  string
	Release *Release
	Asset   Asset
	// Installer is true when the asset is an MSI rather than a bare binary.
	Installer bool
}

// Prepare picks the right asset for this platform.
func Prepare(current string, r *Release) (*Plan, error) {
	if !Available(current, r) {
		return nil, nil
	}
	goos, goarch := runtime.GOOS, runtime.GOARCH
	if msi := MSIName(goos, goarch); msi != "" {
		if a, ok := r.Asset(msi); ok {
			return &Plan{Current: current, Latest: r.Version(), Release: r, Asset: a, Installer: true}, nil
		}
	}
	if a, ok := r.Asset(AssetName("", goos, goarch)); ok {
		return &Plan{Current: current, Latest: r.Version(), Release: r, Asset: a}, nil
	}
	return nil, fmt.Errorf("发布 %s 没有适合 %s/%s 的产物", r.Tag, goos, goarch)
}

// Download fetches the plan's asset into the cache and returns its path.
func (p *Plan) Download(ctx context.Context) (string, error) {
	dir := filepath.Join(appdir.Cache(), "updates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, p.Asset.Name)
	_, err := dl.To(ctx, p.Asset.URL, dst, dl.Options{
		Connections: 8,
		Timeout:     60 * time.Second,
		Progress:    func(done, total int64) {},
	})
	if err != nil {
		return "", err
	}
	if p.Asset.Size > 0 {
		st, err := os.Stat(dst)
		if err != nil {
			return "", err
		}
		if st.Size() != p.Asset.Size {
			return "", fmt.Errorf("下载不完整: %d/%d 字节", st.Size(), p.Asset.Size)
		}
	}
	return dst, nil
}

// Checksums downloads checksums.txt of the release, if published, and returns
// the expected SHA256 of name.
func (p *Plan) Checksums(ctx context.Context) (string, bool) {
	a, ok := p.Release.Asset("checksums.txt")
	if !ok {
		return "", false
	}
	raw, err := fetch(ctx, a.URL)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == p.Asset.Name {
			return strings.ToLower(fields[0]), true
		}
	}
	return "", false
}

// VerifySHA256 checks path against want.
func VerifySHA256(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("校验失败: %s != %s", got, want)
	}
	return nil
}

func fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "translator233-updater")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
