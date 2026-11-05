package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/neko233-com/translator233/internal/appdir"
	"github.com/neko233-com/translator233/internal/translate"
)

type daemonInfo struct {
	Addr string `json:"addr"`
	PID  int    `json:"pid"`
}

func writeDaemonFile(addr string) error {
	if err := appdir.Ensure(); err != nil {
		return err
	}
	b, _ := json.Marshal(daemonInfo{Addr: addr, PID: os.Getpid()})
	return os.WriteFile(appdir.DaemonFile(), b, 0o644)
}

func removeDaemonFile() { os.Remove(appdir.DaemonFile()) }

// viaDaemon sends the request to a running `translator233 serve`, so a one-shot
// CLI call skips loading the model (~0.5s) and answers in milliseconds.
func viaDaemon(req translate.Request, wait time.Duration) (*translate.Result, bool) {
	raw, err := os.ReadFile(appdir.DaemonFile())
	if err != nil {
		return nil, false
	}
	var info daemonInfo
	if err := json.Unmarshal(raw, &info); err != nil || info.Addr == "" {
		return nil, false
	}
	body, _ := json.Marshal(req)
	client := &http.Client{Timeout: wait}
	resp, err := client.Post("http://"+info.Addr+"/v1/translate", "application/json", bytes.NewReader(body))
	if err != nil {
		if os.Getenv("TRANSLATOR233_DEBUG") != "" {
			os.Stderr.WriteString("daemon: " + err.Error() + "\n")
		}
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if os.Getenv("TRANSLATOR233_DEBUG") != "" {
			os.Stderr.WriteString("daemon: http " + resp.Status + "\n")
		}
		return nil, false
	}
	var res translate.Result
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, false
	}
	return &res, true
}
