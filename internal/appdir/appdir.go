// Package appdir resolves the on-disk locations translator233 uses.
package appdir

import (
	"os"
	"path/filepath"
)

const envHome = "TRANSLATOR233_HOME"

// Root is the application data directory (~/.translator233 by default).
func Root() string {
	if v := os.Getenv(envHome); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".translator233"
	}
	return filepath.Join(home, ".translator233")
}

// Packs holds installed language packs.
func Packs() string { return filepath.Join(Root(), "packs") }

// Runtime holds the ONNX Runtime shared library.
func Runtime() string { return filepath.Join(Root(), "runtime") }

// Cache holds transient files.
func Cache() string { return filepath.Join(Root(), "cache") }

// Config is the user settings file.
func Config() string { return filepath.Join(Root(), "config.json") }

// DaemonFile records the address of a running `serve` instance.
func DaemonFile() string { return filepath.Join(Root(), "daemon.json") }

// Ensure creates the standard directory tree.
func Ensure() error {
	for _, d := range []string{Packs(), Runtime(), Cache()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}
