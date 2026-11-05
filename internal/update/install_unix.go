//go:build !windows

package update

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Install replaces the running executable. On Unix the running process keeps
// the old inode, so the swap is safe and takes effect on the next start.
func Install(_ context.Context, path string, installer bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()

	tmp := exe + ".new"
	dst, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("无法写入 %s: %w", tmp, err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(tmp)
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// RelaunchHint explains what happens after Install.
func RelaunchHint() string {
	return "已替换为最新版本，重新启动即可生效。"
}

// ApplyMSI is a no-op outside Windows.
func ApplyMSI(msiPath, logPath string) error { return fmt.Errorf("MSI 只能在 Windows 上安装") }
