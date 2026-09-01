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

type Config struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"baseURL"`
	Model    string `json:"model"`
}

type Request struct {
	System string
	Prompt string
}

type Provider interface {
	Complete(context.Context, Request) (string, error)
}

func NewProvider(config Config, apiKey string) (Provider, error) {
	client := &http.Client{Timeout: 45 * time.Second}
	baseURL := strings.TrimRight(config.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	adapter := httpAdapter{baseURL: baseURL, model: config.Model, apiKey: apiKey, client: client}
	switch config.Provider {
	case "responses":
		return &ResponsesAdapter{httpAdapter: adapter}, nil
	case "chat":
		return &ChatCompletionsAdapter{httpAdapter: adapter}, nil
	default:
		return nil, fmt.Errorf("unsupported AI provider %q", config.Provider)
	}
}

type httpAdapter struct {
	baseURL string
	model   string
	apiKey  string
	client  *http.Client
}

func (a httpAdapter) post(ctx context.Context, path string, payload any, target any) error {
	if strings.TrimSpace(a.model) == "" {
		return fmt.Errorf("AI model is required")
	}
	if strings.TrimSpace(a.apiKey) == "" {
		return fmt.Errorf("AI API key is not configured")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Content-Type", "application/json")
	response, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("AI provider returned %s: %s", response.Status, strings.TrimSpace(string(data)))
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode AI provider response: %w", err)
	}
	return nil
}

// ResponsesAdapter targets the OpenAI Responses API compatible /responses endpoint.
type ResponsesAdapter struct{ httpAdapter }

func (a *ResponsesAdapter) Complete(ctx context.Context, request Request) (string, error) {
	var response struct {
		OutputText string `json:"output_text"`
		Output     []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	err := a.post(ctx, "/responses", map[string]any{"model": a.model, "instructions": request.System, "input": request.Prompt}, &response)
	if err != nil {
		return "", err
	}
	if response.OutputText != "" {
		return response.OutputText, nil
	}
	var result strings.Builder
	for _, output := range response.Output {
		for _, content := range output.Content {
			result.WriteString(content.Text)
		}
	}
	if result.Len() == 0 {
		return "", fmt.Errorf("Responses API returned no text")
	}
	return result.String(), nil
}

// ChatCompletionsAdapter targets OpenAI-compatible /chat/completions endpoints.
type ChatCompletionsAdapter struct{ httpAdapter }

func (a *ChatCompletionsAdapter) Complete(ctx context.Context, request Request) (string, error) {
	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	payload := map[string]any{"model": a.model, "messages": []map[string]string{{"role": "system", "content": request.System}, {"role": "user", "content": request.Prompt}}}
	if err := a.post(ctx, "/chat/completions", payload, &response); err != nil {
		return "", err
	}
	if len(response.Choices) == 0 || response.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("Chat Completions API returned no text")
	}
	return response.Choices[0].Message.Content, nil
}
