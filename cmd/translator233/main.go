// Command translator233 is the command line interface for local, offline
// machine translation. It is designed to be driven by agents as well as humans.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/neko233-com/translator233/internal/lang"
	"github.com/neko233-com/translator233/internal/llm"
	"github.com/neko233-com/translator233/internal/pack"
	"github.com/neko233-com/translator233/internal/rtsetup"
	"github.com/neko233-com/translator233/internal/translate"
)

var version = "1.0.1"

// noAutoInstall disables downloading missing language packs.
var noAutoInstall bool

// engineMode is one of auto, local, llm.
var engineMode = "auto"

const usage = `translator233 — 本地离线翻译引擎（CLI / GUI / HTTP）

用法:
  translator233 translate [文本...] -f auto -t en [--json]
  translator233 translate --file in.txt -t en
  echo "文本" | translator233 translate -t en
  translator233 packs list|install|remove|search ...
  translator233 langs
  translator233 runtime status|install
  translator233 serve --addr 127.0.0.1:8765
  translator233 gui
  translator233 update [--check] [--json]
  translator233 config show|set-llm|clear-llm
  translator233 version

翻译参数:
  -f, --from     源语言（默认 auto）
  -t, --to       目标语言（必填）
      --engine   后端 auto|local|llm（默认 auto：本地优先）
  -j, --json     输出 JSON
      --jsonl    逐行 JSON（stdin 每行一条，适合批量/agent 管道）
      --raw      只输出译文
  -q, --quiet    静默（不输出进度到 stderr）

语言包:
  首次运行自动安装 zh-en / en-zh，其他语向在翻译时按需下载。
  packs install 支持 -c N 指定并发连接数（默认 8）。

环境变量:
  TRANSLATOR233_HOME       数据目录（默认 ~/.translator233）
  DEEPSEEK_API_KEY         在线模型密钥（也可写入 config.json）
  TRANSLATOR233_LLM_BASE_URL / TRANSLATOR233_LLM_MODEL
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "translate", "tr", "t":
		err = cmdTranslate(os.Args[2:])
	case "packs", "pack", "p":
		err = cmdPacks(os.Args[2:])
	case "langs", "lang":
		err = cmdLangs(os.Args[2:])
	case "runtime", "rt":
		err = cmdRuntime(os.Args[2:])
	case "serve":
		err = cmdServe(os.Args[2:])
	case "gui":
		err = cmdGUI(os.Args[2:])
	case "update", "upgrade":
		err = cmdUpdate(os.Args[2:])
	case "config", "cfg":
		err = cmdConfig(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Printf("translator233 %s\n", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		// Bare text is translated with default target English.
		err = cmdTranslate(os.Args[1:])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
		os.Exit(1)
	}
}

func newService(quiet bool) (*translate.Service, error) {
	cfg := llm.LoadConfig()
	return translate.New(translate.Options{
		AutoInstall: !noAutoInstall,
		Progress:    packProgress(quiet),
		Engine:      engineMode,
		LLM:         &cfg,
	})
}

// packProgress renders language pack downloads on stderr, aggregated per pack
// so parallel files share one line.
func packProgress(quiet bool) func(packID, file string, done, total int64) {
	if quiet {
		return nil
	}
	var (
		mu     sync.Mutex
		last   time.Time
		states = map[string]map[string][2]int64{}
	)
	return func(packID, file string, done, total int64) {
		mu.Lock()
		defer mu.Unlock()
		files := states[packID]
		if files == nil {
			files = map[string][2]int64{}
			states[packID] = files
			fmt.Fprintf(os.Stderr, "下载语言包 %s …\n", packID)
		}
		files[file] = [2]int64{done, total}
		var d, t int64
		for _, v := range files {
			d += v[0]
			t += v[1]
		}
		if t <= 0 || (time.Since(last) < 120*time.Millisecond && d < t) {
			return
		}
		last = time.Now()
		fmt.Fprintf(os.Stderr, "\r  %-8s %3d%%  %s / %s", packID, d*100/t, human(d), human(t))
	}
}

// ensureDefaults installs the built-in Chinese/English packs on a fresh install.
func ensureDefaults(svc *translate.Service, quiet bool) {
	if noAutoInstall || engineMode == "llm" || svc.HasPacks() {
		return
	}
	if !quiet {
		fmt.Fprintln(os.Stderr, "首次使用：准备默认语言包（中文 ↔ 英语）…")
	}
	if err := svc.EnsureDefaults(context.Background()); err != nil {
		if !quiet {
			fmt.Fprintln(os.Stderr, "默认语言包安装失败（可稍后用 packs install 重试）: "+err.Error())
		}
	}
}

// ensureRuntime installs the ONNX Runtime on first use.
func ensureRuntime(quiet bool) error {
	if rtsetup.Installed() {
		return nil
	}
	if !quiet {
		fmt.Fprintln(os.Stderr, "首次运行：下载 ONNX Runtime "+rtsetup.Version+" …")
	}
	_, err := rtsetup.Ensure(context.Background(), progressPrinter(quiet))
	return err
}

func progressPrinter(quiet bool) func(done, total int64) {
	if quiet {
		return nil
	}
	last := int64(-1)
	return func(done, total int64) {
		if total <= 0 {
			return
		}
		pct := done * 100 / total
		if pct != last && pct%10 == 0 {
			last = pct
			fmt.Fprintf(os.Stderr, "\r  %3d%% %s/%s", pct, human(done), human(total))
			if pct >= 100 {
				fmt.Fprintln(os.Stderr)
			}
		}
	}
}

func human(n int64) string {
	f := float64(n)
	switch {
	case f >= 1<<30:
		return fmt.Sprintf("%.1fGB", f/(1<<30))
	case f >= 1<<20:
		return fmt.Sprintf("%.1fMB", f/(1<<20))
	case f >= 1<<10:
		return fmt.Sprintf("%.1fKB", f/(1<<10))
	}
	return fmt.Sprintf("%dB", n)
}

func cmdTranslate(args []string) error {
	fs := flag.NewFlagSet("translate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		from     = fs.String("from", "auto", "源语言")
		to       = fs.String("to", "", "目标语言")
		asJSON   = fs.Bool("json", false, "JSON 输出")
		jsonl    = fs.Bool("jsonl", false, "每行一条 JSON")
		raw      = fs.Bool("raw", false, "只输出译文")
		quiet    = fs.Bool("quiet", false, "静默")
		file     = fs.String("file", "", "从文件读取")
		noDaemon = fs.Bool("no-daemon", false, "不使用后台服务")
	)
	fs.BoolVar(&noAutoInstall, "no-auto-install", false, "缺少语言包时不自动下载")
	fs.StringVar(&engineMode, "engine", "auto", "后端: auto|local|llm")
	fs.StringVar(from, "f", "auto", "源语言")
	fs.StringVar(to, "t", "", "目标语言")
	fs.BoolVar(asJSON, "j", false, "JSON 输出")
	fs.BoolVar(quiet, "q", false, "静默")
	if err := fs.Parse(reorder(args)); err != nil {
		return err
	}
	if *to == "" {
		return errors.New("必须指定目标语言，例如 -t en")
	}
	if err := ensureRuntime(*quiet); err != nil {
		return err
	}
	if *jsonl {
		svc, err := newService(*quiet)
		if err != nil {
			return err
		}
		defer svc.Close()
		ensureDefaults(svc, *quiet)
		return runJSONL(svc, *from, *to, *quiet)
	}

	text, err := readInput(*file, fs.Args())
	if err != nil {
		return err
	}
	if text == "" {
		return errors.New("没有输入文本")
	}

	// A resident `serve` answers in milliseconds; fall back to in-process.
	if !*noDaemon {
		if dr, ok := viaDaemon(translate.Request{Text: text, Source: *from, Target: *to}, 30*time.Second); ok {
			return printResult(dr, *asJSON, *raw, *quiet)
		}
	}

	svc, err := newService(*quiet)
	if err != nil {
		return err
	}
	defer svc.Close()
	ensureDefaults(svc, *quiet)

	res, err := svc.Translate(context.Background(), translate.Request{Text: text, Source: *from, Target: *to})
	if err != nil {
		return err
	}
	return printResult(res, *asJSON, *raw, *quiet)
}

func printResult(res *translate.Result, asJSON, raw, quiet bool) error {
	switch {
	case asJSON:
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		return enc.Encode(res)
	default:
		fmt.Println(res.Text)
		if !quiet && !raw {
			fmt.Fprintf(os.Stderr, "[%s→%s %s %.0fms %d段]\n", res.Source, res.Target, res.Pack, res.Millis, res.Segments)
		}
	}
	return nil
}

func runJSONL(svc *translate.Service, from, to string, quiet bool) error {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64<<10), 8<<20)
	out := json.NewEncoder(os.Stdout)
	out.SetEscapeHTML(false)
	for in.Scan() {
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		res, err := svc.Translate(context.Background(), translate.Request{Text: line, Source: from, Target: to})
		if err != nil {
			out.Encode(map[string]any{"error": err.Error(), "input": line})
			continue
		}
		out.Encode(res)
	}
	return in.Err()
}

// reorder moves positional arguments behind flags so that
// `translate "text" -t en` parses the same as `translate -t en "text"`.
func reorder(args []string) []string {
	valueFlags := map[string]bool{
		"-f": true, "--f": true, "-from": true, "--from": true,
		"-t": true, "--t": true, "-to": true, "--to": true,
		"-file": true, "--file": true,
		"-engine": true, "--engine": true,
	}
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if len(a) > 1 && strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			name := a
			if k := strings.IndexByte(a, '='); k >= 0 {
				name = a[:k]
			}
			if valueFlags[name] && !strings.Contains(a, "=") && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

// splitConns pulls `-c N` / `--connections N` out of args.
func splitConns(args []string) ([]string, int) {
	conns := 0
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-c" || a == "--connections":
			if i+1 < len(args) {
				i++
				fmt.Sscanf(args[i], "%d", &conns)
			}
		case strings.HasPrefix(a, "--connections="):
			fmt.Sscanf(strings.TrimPrefix(a, "--connections="), "%d", &conns)
		default:
			out = append(out, a)
		}
	}
	return out, conns
}

func readInput(file string, args []string) (string, error) {
	if file != "" {
		b, err := os.ReadFile(file)
		return string(b), err
	}
	if len(args) > 0 {
		return strings.Join(args, " "), nil
	}
	st, err := os.Stdin.Stat()
	if err != nil {
		return "", err
	}
	if st.Mode()&os.ModeCharDevice != 0 {
		return "", nil
	}
	b, err := io.ReadAll(os.Stdin)
	return string(b), err
}

func cmdPacks(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: translator233 packs list|install|remove|search")
	}
	cat, err := pack.LoadCatalog()
	if err != nil {
		return err
	}
	store := pack.DefaultStore()
	switch args[0] {
	case "list", "ls":
		installed, err := store.List()
		if err != nil {
			return err
		}
		if len(installed) == 0 {
			fmt.Println("未安装任何语言包。可用: translator233 packs search")
			return nil
		}
		for _, p := range installed {
			fmt.Printf("%-8s %-24s %8s  %s\n", p.ID, p.Name, human(p.Bytes), p.Path)
		}
	case "search", "find":
		kw := strings.ToLower(strings.Join(args[1:], " "))
		for _, p := range cat.Packs {
			if kw != "" && !strings.Contains(strings.ToLower(p.ID), kw) && !strings.Contains(p.Name, kw) {
				continue
			}
			mark := " "
			if store.IsInstalled(p) {
				mark = "*"
			}
			fmt.Printf("%s %-8s %-24s %8s\n", mark, p.ID, p.Name, human(p.TotalSize()))
		}
	case "install", "add":
		ids, conns := splitConns(args[1:])
		if len(ids) == 0 {
			return errors.New("用法: translator233 packs install <id> [id...] [-c 8]")
		}
		store.Connections = conns
		agg := packProgress(false)
		for _, id := range ids {
			p, ok := cat.Get(id)
			if !ok {
				return fmt.Errorf("未知语言包 %s", id)
			}
			if store.IsInstalled(p) {
				fmt.Printf("%s 已安装\n", p.ID)
				continue
			}
			fmt.Printf("安装 %s (%s) …\n", p.ID, human(p.TotalSize()))
			err := store.Install(context.Background(), p, func(file string, done, total int64) {
				agg(p.ID, file, done, total)
			})
			fmt.Fprintln(os.Stderr)
			if err != nil {
				return err
			}
			fmt.Printf("%s 安装完成\n", p.ID)
		}
	case "remove", "rm":
		if len(args) < 2 {
			return errors.New("用法: translator233 packs remove <id>")
		}
		for _, id := range args[1:] {
			if err := store.Remove(id); err != nil {
				return err
			}
			fmt.Printf("已删除 %s\n", id)
		}
	default:
		return fmt.Errorf("未知子命令 %q", args[0])
	}
	return nil
}

func cmdLangs(args []string) error {
	cat, err := pack.LoadCatalog()
	if err != nil {
		return err
	}
	store := pack.DefaultStore()
	for _, p := range cat.Packs {
		mark := " "
		if store.IsInstalled(p) {
			mark = "*"
		}
		fmt.Printf("%s %-8s %-12s → %-12s %s\n", mark, p.ID, lang.Name(p.Source), lang.Name(p.Target), human(p.TotalSize()))
	}
	fmt.Println("\n* = 已安装；任意两语言可经 en 中转（pivot）。")
	return nil
}

func cmdRuntime(args []string) error {
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "status":
		if rtsetup.Installed() {
			fmt.Println("已安装:", rtsetup.Path())
		} else {
			fmt.Println("未安装（首次翻译时自动下载）")
		}
	case "install":
		if err := ensureRuntime(false); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr)
		fmt.Println("已安装:", rtsetup.Path())
	default:
		return fmt.Errorf("未知子命令 %q", sub)
	}
	return nil
}
