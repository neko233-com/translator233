// Package llm translates through an OpenAI-compatible chat completion API
// (DeepSeek, Ollama, vLLM, ...). It is optional: the local ONNX engine stays
// the default, this is the "better quality, needs network" escape hatch.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/neko233-com/translator233/internal/appdir"
	"github.com/neko233-com/translator233/internal/lang"
)

// Config is the on-disk / environment configuration.
type Config struct {
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	APIKey  string `json:"api_key,omitempty"`
	Timeout int    `json:"timeout_seconds,omitempty"`
}

// DefaultModel is used when the config does not name one.
const DefaultModel = "deepseek-chat"

// LoadConfig reads ~/.translator233/config.json and applies environment
// overrides. The API key is never taken from the repository.
func LoadConfig() Config {
	c := Config{}
	if raw, err := os.ReadFile(filepath.Join(appdir.Root(), "config.json")); err == nil {
		var file struct {
			LLM *Config `json:"llm"`
		}
		if json.Unmarshal(raw, &file) == nil && file.LLM != nil {
			c = *file.LLM
		}
	}
	if v := os.Getenv("TRANSLATOR233_LLM_BASE_URL"); v != "" {
		c.BaseURL = v
	}
	if v := os.Getenv("TRANSLATOR233_LLM_MODEL"); v != "" {
		c.Model = v
	}
	if v := os.Getenv("TRANSLATOR233_LLM_KEY"); v != "" {
		c.APIKey = v
	}
	if c.APIKey == "" {
		c.APIKey = os.Getenv("DEEPSEEK_API_KEY")
	}
	if c.BaseURL == "" {
		c.BaseURL = os.Getenv("OPENAI_BASE_URL")
	}
	if c.Model == "" {
		c.Model = DefaultModel
	}
	return c
}

// Enabled reports whether a usable endpoint is configured.
func (c Config) Enabled() bool { return c.BaseURL != "" && c.APIKey != "" }

// Save writes the configuration to the user's config.json with 0600 permissions.
func (c Config) Save() error {
	if err := appdir.Ensure(); err != nil {
		return err
	}
	path := filepath.Join(appdir.Root(), "config.json")
	doc := map[string]any{"llm": c}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

// Translate renders text through the chat API.
func (c Config) Translate(ctx context.Context, text, source, target string) (string, error) {
	if !c.Enabled() {
		return "", fmt.Errorf("未配置在线模型：请设置 DEEPSEEK_API_KEY 或运行 translator233 config set-llm")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 60
	}
	body := map[string]any{
		"model":       c.Model,
		"temperature": 0,
		"stream":      false,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt(source, target)},
			{"role": "user", "content": text},
		},
	}
	raw, _ := json.Marshal(body)
	url := strings.TrimSuffix(c.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm: http %d: %s", resp.StatusCode, truncate(string(payload), 300))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return "", fmt.Errorf("llm: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("llm: empty response")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

func systemPrompt(source, target string) string {
	from := "the detected source language"
	if source != "" && source != "auto" {
		from = lang.Name(source)
	}
	return fmt.Sprintf(
		"You are a professional translation engine. Translate the user's text from %s (%s) into %s (%s). "+
			"Reply with the translation only: no explanations, no quotes, no markdown fences. "+
			"Keep line breaks, numbers, code and placeholders exactly as they are.",
		from, source, lang.Name(target), target)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
