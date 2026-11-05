// Package e2e runs the real CLI against real language packs.
//
// It downloads the ONNX Runtime and the Chinese/English packs on first run
// (~300MB), so it only runs when explicitly asked for:
//
//	TRANSLATOR233_E2E=1 go test ./e2e -v -timeout 30m
//
// TRANSLATOR233_E2E_HOME can point at an existing data directory to skip the
// downloads (for example your normal ~/.translator233).
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var (
	cliPath string
	homeDir string
)

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func TestMain(m *testing.M) {
	if os.Getenv("TRANSLATOR233_E2E") == "" {
		fmt.Fprintln(os.Stderr, "skipping e2e: set TRANSLATOR233_E2E=1 to run")
		os.Exit(0)
	}
	tmp, err := os.MkdirTemp("", "translator233-e2e-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)

	cliPath = filepath.Join(tmp, "translator233"+exeSuffix())
	build := exec.Command("go", "build", "-o", cliPath, "./cmd/translator233")
	build.Dir = ".."
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		panic(err)
	}

	homeDir = os.Getenv("TRANSLATOR233_E2E_HOME")
	if homeDir == "" {
		homeDir = filepath.Join(tmp, "home")
	}
	os.Exit(m.Run())
}

type result struct {
	Text     string  `json:"text"`
	Source   string  `json:"source"`
	Target   string  `json:"target"`
	Pack     string  `json:"pack"`
	Engine   string  `json:"engine"`
	Segments int     `json:"segments"`
	Millis   float64 `json:"ms"`
	Error    string  `json:"error"`
}

func run(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(cliPath, args...)
	cmd.Env = append(os.Environ(), "TRANSLATOR233_HOME="+homeDir)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

func TestTranslateToEnglish(t *testing.T) {
	out, errb, err := run(t, "", "translate", "你好，世界！", "-t", "en", "--json", "-q")
	if err != nil {
		t.Fatalf("translate failed: %v\nstderr: %s", err, errb)
	}
	var res result
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("bad json %q: %v", out, err)
	}
	if strings.TrimSpace(res.Text) == "" {
		t.Fatalf("empty translation: %+v", res)
	}
	if res.Source != "zh" || res.Target != "en" || res.Pack != "zh-en" {
		t.Fatalf("unexpected metadata: %+v", res)
	}
	if res.Engine != "local" {
		t.Fatalf("engine = %q, want local", res.Engine)
	}
	if !strings.Contains(strings.ToLower(res.Text), "hello") {
		t.Fatalf("suspicious translation: %q", res.Text)
	}
}

func TestTranslateFromStdinJSONL(t *testing.T) {
	out, errb, err := run(t, "你好，世界\n今天天气不错。\n", "translate", "-t", "en", "--jsonl", "-q")
	if err != nil {
		t.Fatalf("jsonl failed: %v\nstderr: %s", err, errb)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 result lines, got %d: %q", len(lines), out)
	}
	for _, line := range lines {
		var res result
		if err := json.Unmarshal([]byte(line), &res); err != nil {
			t.Fatalf("bad json line %q: %v", line, err)
		}
		if res.Error != "" {
			t.Fatalf("line failed: %s", res.Error)
		}
		if strings.TrimSpace(res.Text) == "" {
			t.Fatalf("empty translation in %q", line)
		}
	}
}

func TestLanguageDetection(t *testing.T) {
	out, errb, err := run(t, "", "translate", "The quick brown fox jumps over the lazy dog.", "-t", "en", "--json", "-q")
	if err != nil {
		t.Fatalf("translate failed: %v\nstderr: %s", err, errb)
	}
	var res result
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.Source != "en" {
		t.Fatalf("source detection = %q, want en", res.Source)
	}
}

func TestPacksList(t *testing.T) {
	out, errb, err := run(t, "", "packs", "list")
	if err != nil {
		t.Fatalf("packs list failed: %v\nstderr: %s", err, errb)
	}
	if !strings.Contains(out, "zh-en") || !strings.Contains(out, "en-zh") {
		t.Fatalf("default packs missing from list:\n%s", out)
	}
}

func TestServeHTTP(t *testing.T) {
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	cmd := exec.Command(cliPath, "serve", "--addr", addr)
	cmd.Env = append(os.Environ(), "TRANSLATOR233_HOME="+homeDir)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	base := "http://" + addr
	waitHealthy(t, base, 90*time.Second)

	body := `{"text":"你好，世界！","target":"en"}`
	resp, err := http.Post(base+"/v1/translate", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var res result
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(res.Text) == "" || res.Pack != "zh-en" {
		t.Fatalf("unexpected result: %+v", res)
	}

	// The CLI must reuse the running server (daemon.json) and stay fast.
	start := time.Now()
	out, stderr, err := run(t, "", "translate", "你好，世界！", "-t", "en", "--raw", "-q")
	if err != nil {
		t.Fatalf("cli via daemon failed: %v\n%s", err, stderr)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("empty output via daemon")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("daemon path took %v, expected a fast reply", elapsed)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func waitHealthy(t *testing.T, base string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("server at %s did not become healthy", base)
}
