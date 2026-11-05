package main

import (
	"os"
	"path/filepath"
	"time"

	"github.com/neko233-com/translator233/internal/appdir"
	"github.com/neko233-com/translator233/internal/update"
)

// runApplyUpdate is the helper path: a copy of the CLI in %TEMP% installs the
// downloaded MSI once the original process has exited.
func runApplyUpdate(msi string) int {
	time.Sleep(1500 * time.Millisecond)
	logPath := ""
	if appdir.Ensure() == nil {
		logPath = filepath.Join(appdir.Cache(), "update.log")
	}
	if err := update.ApplyMSI(msi, logPath); err != nil {
		return 1
	}
	return 0
}

func init() {
	if len(os.Args) >= 3 && os.Args[1] == "--apply-update" {
		os.Exit(runApplyUpdate(os.Args[2]))
	}
}
