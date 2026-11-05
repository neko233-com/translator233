package translate

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

func libExt() string {
	switch runtime.GOOS {
	case "windows":
		return ".dll"
	case "darwin":
		return ".dylib"
	}
	return ".so"
}

func defaultThreads() int {
	if v := os.Getenv("TRANSLATOR233_THREADS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	// Marian models are small: past 4 threads the synchronization cost cancels
	// the gain, and extra threads starve concurrent requests.
	n := runtime.NumCPU()
	if n > 4 {
		n = 4
	}
	if n < 1 {
		n = 1
	}
	return n
}

// findLibrary locates the ONNX Runtime shared library under dir.
func findLibrary(dir string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("未找到 ONNX Runtime，请运行 translator233 runtime install")
	}
	ext := libExt()
	var found string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := strings.ToLower(d.Name())
		if strings.Contains(name, "onnxruntime") && strings.HasSuffix(name, ext) {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("未找到 ONNX Runtime 运行库 (%s)", dir)
	}
	return found, nil
}
