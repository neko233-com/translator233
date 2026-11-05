package rtsetup

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
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

func TestWantedSkipsDebugSymbols(t *testing.T) {
	const dylib = "onnxruntime-osx-arm64-1.31.1/lib/libonnxruntime.1.31.1.dylib"
	const dwarf = "onnxruntime-osx-arm64-1.31.1/lib/libonnxruntime.1.31.1.dylib.dSYM/Contents/Resources/DWARF/libonnxruntime.1.31.1.dylib"
	if !wantedFor("darwin", dylib) {
		t.Fatal("the real dylib must match")
	}
	if wantedFor("darwin", dwarf) {
		t.Fatal("the dSYM DWARF file must not match")
	}
	if !wantedFor("windows", "onnxruntime-win-x64-1.31.1/lib/onnxruntime.dll") {
		t.Fatal("windows dll must match")
	}
}

func TestExtractTarPicksTheLibrary(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "ort.tgz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	add := func(name, body string) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	add("onnxruntime-osx-arm64-1.31.1/lib/libonnxruntime.1.31.1.dylib", "REAL")
	add("onnxruntime-osx-arm64-1.31.1/lib/libonnxruntime.1.31.1.dylib.dSYM/Contents/Resources/DWARF/libonnxruntime.1.31.1.dylib", "SYMBOLS")
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	out := t.TempDir()
	if err := extractTarGzFor(archive, out, "darwin", "libonnxruntime.1.31.1.dylib"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(out, "libonnxruntime.1.31.1.dylib"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "REAL" {
		t.Fatalf("extracted %q, want the real library", got)
	}
}

// TestRealMacArchive extracts the published macOS archive when ORT_OSX_ARCHIVE
// points at it, and checks the payload is the library, not its debug symbols.
func TestRealMacArchive(t *testing.T) {
	archive := os.Getenv("ORT_OSX_ARCHIVE")
	if archive == "" {
		t.Skip("ORT_OSX_ARCHIVE not set")
	}
	out := t.TempDir()
	name := "libonnxruntime.1.31.1.dylib"
	if err := extractTarGzFor(archive, out, "darwin", name); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(out, name))
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() < 20<<20 {
		t.Fatalf("extracted %d bytes, looks like the dSYM bundle", st.Size())
	}
}
