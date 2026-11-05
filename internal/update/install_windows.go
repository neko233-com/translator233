//go:build windows

package update

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// detachedProcess starts the helper without inheriting our console.
const detachedProcess = 0x00000008 // DETACHED_PROCESS

// Install hands the MSI to a temporary copy of the CLI, because the installer
// must replace the very executable that is running now: the copy is not in the
// install directory, so nothing is locked while msiexec works.
func Install(_ context.Context, path string, installer bool) error {
	if !installer {
		return fmt.Errorf("Windows 上只支持通过 MSI 更新，请从发布页下载安装包")
	}
	helper, err := helperPath()
	if err != nil {
		return err
	}
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("translator233-update-%d.exe", time.Now().UnixNano()))
	if err := copyFile(helper, tmp); err != nil {
		return fmt.Errorf("准备更新程序失败: %w", err)
	}
	cmd := exec.Command(tmp, "--apply-update", path)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: detachedProcess | syscall.CREATE_NEW_PROCESS_GROUP,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// helperPath finds the command line executable to copy.
func helperPath() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	if strings.EqualFold(filepath.Base(self), "translator233.exe") {
		return self, nil
	}
	// The window ships next to the CLI; it updates through it.
	cli := filepath.Join(filepath.Dir(self), "translator233.exe")
	if _, err := os.Stat(cli); err == nil {
		return cli, nil
	}
	if _, err := os.Stat(self); err == nil {
		return self, nil
	}
	return "", fmt.Errorf("找不到 translator233.exe，无法完成更新")
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// RelaunchHint explains what happens after Install.
func RelaunchHint() string {
	return "安装程序将在后台静默完成，请稍候再重新启动 translator233。"
}

// ApplyMSI runs the installer and waits for it; used by the helper process.
func ApplyMSI(msiPath, logPath string) error {
	args := []string{"/i", msiPath, "/qn", "/norestart"}
	if logPath != "" {
		args = append(args, "/l*v", logPath)
	}
	cmd := exec.Command("msiexec.exe", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run()
}
