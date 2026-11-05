//go:build windows

package update

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"
)

// detachedProcess starts the installer without inheriting our console.
const detachedProcess = 0x00000008 // DETACHED_PROCESS

// Install runs the downloaded MSI silently. Because the running executable is
// locked, msiexec is started from a detached shell that waits for this process
// to exit first.
func Install(_ context.Context, path string, installer bool) error {
	if !installer {
		return fmt.Errorf("Windows 上只支持通过 MSI 更新，请从发布页下载安装包")
	}
	script := fmt.Sprintf(`ping -n 3 127.0.0.1 >nul & msiexec.exe /i "%s" /qn /norestart`, path)
	cmd := exec.Command("cmd.exe", "/c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: detachedProcess | syscall.CREATE_NEW_PROCESS_GROUP,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// RelaunchHint explains what happens after Install.
func RelaunchHint() string {
	return "安装程序将在本进程退出后静默运行，完成后从开始菜单重新打开即可。"
}
