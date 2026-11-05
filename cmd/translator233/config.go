package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/neko233-com/translator233/internal/appdir"
	"github.com/neko233-com/translator233/internal/llm"
)

// cmdConfig manages the optional online (OpenAI-compatible) backend.
func cmdConfig(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: translator233 config show|set-llm|clear-llm")
	}
	switch args[0] {
	case "show":
		cfg := llm.LoadConfig()
		fmt.Println("配置文件:", appdir.Config())
		fmt.Println("base_url:", orDash(cfg.BaseURL))
		fmt.Println("model:   ", orDash(cfg.Model))
		if cfg.APIKey == "" {
			fmt.Println("api_key: (未设置)")
		} else {
			fmt.Println("api_key:", mask(cfg.APIKey))
		}
		fmt.Println("可用:    ", cfg.Enabled())
	case "set-llm":
		cfg := llm.LoadConfig()
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--base-url":
				if i+1 < len(args) {
					i++
					cfg.BaseURL = args[i]
				}
			case "--model":
				if i+1 < len(args) {
					i++
					cfg.Model = args[i]
				}
			case "--key":
				if i+1 < len(args) {
					i++
					cfg.APIKey = args[i]
				}
			}
		}
		cfg.BaseURL = strings.TrimSuffix(cfg.BaseURL, "/")
		if cfg.BaseURL == "" {
			return errors.New("必须提供 --base-url，例如 https://api.deepseek.com")
		}
		if cfg.Model == "" {
			cfg.Model = llm.DefaultModel
		}
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Println("已写入", appdir.Config(), "（权限 0600，不在仓库内）")
		fmt.Println("提示：也可以改用环境变量 DEEPSEEK_API_KEY，避免密钥落盘。")
		if cfg.APIKey != "" {
			fmt.Println("当前密钥:", mask(cfg.APIKey))
		}
	case "clear-llm":
		cfg := llm.Config{}
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Println("已清除在线模型配置")
	default:
		return fmt.Errorf("未知子命令 %q", args[0])
	}
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func mask(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "…" + s[len(s)-4:]
}
