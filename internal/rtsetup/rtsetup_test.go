package rtsetup

import (
	"strings"
	"testing"
)

func TestAssetNames(t *testing.T) {
	cases := []struct {
		goos, goarch string
		want         string
		kind         string
	}{
		{"windows", "amd64", "onnxruntime-win-x64-1.31.1.zip", "zip"},
		{"windows", "arm64", "onnxruntime-win-arm64-1.31.1.zip", "zip"},
		{"darwin", "arm64", "onnxruntime-osx-arm64-1.31.1.tgz", "tgz"},
	}
	for _, c := range cases {
		name, kind, err := assetFor(c.goos, c.goarch, "1.31.1")
		if err != nil {
			t.Fatalf("%s/%s: %v", c.goos, c.goarch, err)
		}
		if name != c.want || kind != c.kind {
			t.Errorf("%s/%s = %q (%s), want %q (%s)", c.goos, c.goarch, name, kind, c.want, c.kind)
		}
	}
}

func TestAssetRejectsUnsupported(t *testing.T) {
	cases := [][2]string{
		{"linux", "amd64"},
		{"windows", "386"},
		{"darwin", "amd64"},
	}
	for _, c := range cases {
		if _, _, err := assetFor(c[0], c[1], "1.31.1"); err == nil {
			t.Errorf("%s/%s should be rejected", c[0], c[1])
		}
	}
}

func TestLibNameMatchesPlatform(t *testing.T) {
	name := LibName()
	switch {
	case strings.HasSuffix(name, ".dll"), strings.HasSuffix(name, ".dylib"), strings.Contains(name, ".so."):
	default:
		t.Fatalf("unexpected library name %q", name)
	}
}
