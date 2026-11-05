// Package rtsetup downloads and unpacks the ONNX Runtime native library used by
// the inference engine, preferring GitHub mirrors.
package rtsetup

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/neko233-com/translator233/internal/appdir"
	"github.com/neko233-com/translator233/internal/dl"
)

// Version is the pinned ONNX Runtime release; it must match ort.APIVersion.
const Version = "1.31.1"

// LibName is the shared library file name for the current platform.
func LibName() string {
	switch runtime.GOOS {
	case "windows":
		return "onnxruntime.dll"
	case "darwin":
		return "libonnxruntime." + Version + ".dylib"
	default:
		return "libonnxruntime.so." + Version
	}
}

// Path returns the expected location of the shared library.
func Path() string { return filepath.Join(appdir.Runtime(), "onnxruntime-"+Version, LibName()) }

// Installed reports whether the library is already available.
func Installed() bool {
	st, err := os.Stat(Path())
	return err == nil && st.Size() > 0
}

func asset() (string, string, error) {
	switch runtime.GOOS {
	case "windows":
		arch, ok := map[string]string{"amd64": "x64", "arm64": "arm64", "386": "x86"}[runtime.GOARCH]
		if !ok {
			return "", "", fmt.Errorf("unsupported windows architecture %q", runtime.GOARCH)
		}
		return fmt.Sprintf("onnxruntime-win-%s-%s.zip", arch, Version), "zip", nil
	case "linux":
		arch, ok := map[string]string{"amd64": "x64", "arm64": "aarch64"}[runtime.GOARCH]
		if !ok {
			return "", "", fmt.Errorf("unsupported linux architecture %q", runtime.GOARCH)
		}
		return fmt.Sprintf("onnxruntime-linux-%s-%s.tgz", arch, Version), "tgz", nil
	case "darwin":
		if runtime.GOARCH != "arm64" {
			return "", "", fmt.Errorf("ONNX Runtime %s 不再提供 macOS %s 预编译包", Version, runtime.GOARCH)
		}
		return fmt.Sprintf("onnxruntime-osx-arm64-%s.tgz", Version), "tgz", nil
	}
	return "", "", fmt.Errorf("unsupported platform %s/%s", runtime.GOOS, runtime.GOARCH)
}

// Ensure downloads the runtime if it is missing and returns the library path.
func Ensure(ctx context.Context, progress func(done, total int64)) (string, error) {
	if Installed() {
		return Path(), nil
	}
	name, kind, err := asset()
	if err != nil {
		return "", err
	}
	if err := appdir.Ensure(); err != nil {
		return "", err
	}
	url := "https://github.com/microsoft/onnxruntime/releases/download/v" + Version + "/" + name
	tmp := filepath.Join(appdir.Cache(), name)
	if err := os.MkdirAll(filepath.Dir(tmp), 0o755); err != nil {
		return "", err
	}
	if _, err := dl.To(ctx, url, tmp, dl.Options{Progress: progress, Timeout: 60e9}); err != nil {
		return "", fmt.Errorf("download onnxruntime: %w", err)
	}
	defer os.Remove(tmp)

	dstDir := filepath.Dir(Path())
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return "", err
	}
	var extractErr error
	if kind == "zip" {
		extractErr = extractZip(tmp, dstDir)
	} else {
		extractErr = extractTarGz(tmp, dstDir)
	}
	if extractErr != nil {
		return "", extractErr
	}
	if !Installed() {
		return "", fmt.Errorf("onnxruntime %s not found after extracting %s", LibName(), name)
	}
	return Path(), nil
}

func wanted(name string) bool {
	base := filepath.Base(name)
	switch runtime.GOOS {
	case "windows":
		return strings.EqualFold(base, "onnxruntime.dll")
	case "darwin":
		return strings.HasPrefix(base, "libonnxruntime") && strings.HasSuffix(base, ".dylib")
	default:
		return strings.HasPrefix(base, "libonnxruntime.so")
	}
}

func extractZip(zipPath, dstDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		if f.FileInfo().IsDir() || !wanted(f.Name) {
			continue
		}
		out := filepath.Join(dstDir, LibName())
		if err := writeFile(out, func(w io.Writer) error {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()
			_, err = io.Copy(w, rc)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

func extractTarGz(tgzPath, dstDir string) error {
	f, err := os.Open(tgzPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg || !wanted(hdr.Name) {
			continue
		}
		out := filepath.Join(dstDir, LibName())
		body := tr
		if err := writeFile(out, func(w io.Writer) error {
			_, err := io.Copy(w, body)
			return err
		}); err != nil {
			return err
		}
	}
}

func writeFile(dst string, fill func(io.Writer) error) error {
	tmp := dst + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := fill(f); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
