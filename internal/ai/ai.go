// Package ai abstracts LLM providers. AI is optional: nothing in the
// application requires it, and AI never writes YAML that is deployed
// directly — proposals always flow through the IR and human review.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Message is one chat message.
type Message struct {
	Role    string `json:"role"` // system | user | assistant
	Content string `json:"content"`
}

// Provider generates completions. OpenAI-compatible APIs are the first
// target; other providers can implement this interface later.
type Provider interface {
	Complete(ctx context.Context, messages []Message) (string, error)
}

// Config configures an OpenAI-compatible provider.
type Config struct {
	Endpoint string            // base URL, e.g. http://localhost:11434/v1
	Model    string            // model name
	APIKey   string            // bearer token; may be empty for local servers
	Headers  map[string]string // optional extra headers
	// Temperature is optional; nil leaves the provider default in place.
	Temperature *float64
}

// Validate checks the config is usable.
func (c Config) Validate() error {
	if c.Endpoint == "" {
		return fmt.Errorf("AI endpoint is not configured")
	}
	if c.Model == "" {
		return fmt.Errorf("AI model is not configured")
	}
	if !strings.HasPrefix(c.Endpoint, "http://") && !strings.HasPrefix(c.Endpoint, "https://") {
		return fmt.Errorf("AI endpoint must be an http(s) URL")
	}
	return nil
}

// OpenAIProvider talks to OpenAI-compatible /chat/completions endpoints
// (OpenAI, Ollama, llama.cpp, vLLM, LM Studio, ...).
type OpenAIProvider struct {
	cfg    Config
	client *http.Client
}

// NewOpenAIProvider builds a provider with a sane default HTTP client.
func NewOpenAIProvider(cfg Config) *OpenAIProvider {
	return &OpenAIProvider{
		cfg: cfg,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// NewOpenAIProviderWith injects an HTTP client for tests.
func NewOpenAIProviderWith(cfg Config, client *http.Client) *OpenAIProvider {
	return &OpenAIProvider{cfg: cfg, client: client}
}

type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (p *OpenAIProvider) Complete(ctx context.Context, messages []Message) (string, error) {
	if err := p.cfg.Validate(); err != nil {
		return "", err
	}
	reqBody := chatRequest{
		Model:    p.cfg.Model,
		Messages: messages,
	}
	if p.cfg.Temperature != nil {
		reqBody.Temperature = *p.cfg.Temperature
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}
	url := strings.TrimRight(p.cfg.Endpoint, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	}
	for k, v := range p.cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("AI request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return "", fmt.Errorf("AI response was not valid JSON (HTTP %d): %s", resp.StatusCode, truncate(string(raw), 300))
	}
	if cr.Error != nil {
		return "", fmt.Errorf("AI provider error: %s", cr.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("AI provider returned HTTP %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}
	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("AI provider returned no choices")
	}
	return cr.Choices[0].Message.Content, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
