package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/neko233-com/translator233/internal/api"
)

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8765", "监听地址")
	open := fs.Bool("open", false, "启动后打开浏览器")
	fs.StringVar(&engineMode, "engine", "auto", "后端: auto|local|llm")
	fs.BoolVar(&noAutoInstall, "no-auto-install", false, "缺少语言包时不自动下载")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := ensureRuntime(false); err != nil {
		return err
	}
	svc, err := newService(false)
	if err != nil {
		return err
	}
	defer svc.Close()
	ensureDefaults(svc, false)

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	url := "http://" + ln.Addr().String()
	srv := &http.Server{Handler: api.New(svc, version).Handler(), ReadHeaderTimeout: 10 * time.Second}
	fmt.Printf("translator233 %s 监听 %s\n", version, url)
	if *open {
		openBrowser(url)
	}
	if err := writeDaemonFile(ln.Addr().String()); err == nil {
		defer removeDaemonFile()
	}
	return srv.Serve(ln)
}

func cmdGUI(args []string) error {
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	dir, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(dir), "translator233-gui"+exe)
		if _, statErr := os.Stat(candidate); statErr == nil {
			cmd := exec.Command(candidate, args...)
			return cmd.Start()
		}
	}
	return errors.New("未找到 translator233-gui，请先构建它: go build ./cmd/translator233-gui")
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
