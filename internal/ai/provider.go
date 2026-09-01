package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Config struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"baseURL"`
	Model    string `json:"model"`
}

type Request struct {
	System      string
	Prompt      string
	Attachments []Attachment
}

type Attachment struct {
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
	DataURL   string `json:"dataURL"`
	Size      int    `json:"size"`
}

type Provider interface {
	Complete(context.Context, Request) (string, error)
}

func NewProvider(config Config, apiKey string) (Provider, error) {
	client := &http.Client{Timeout: 45 * time.Second}
	baseURL := normalizeBaseURL(config.BaseURL)
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

func normalizeBaseURL(value string) string {
	baseURL := strings.TrimRight(strings.TrimSpace(value), "/")
	if baseURL == "" {
		return "https://api.openai.com/v1"
	}
	parsed, err := url.Parse(baseURL)
	if err == nil && strings.EqualFold(parsed.Host, "api.openai.com") && (parsed.Path == "" || parsed.Path == "/") {
		return baseURL + "/v1"
	}
	return baseURL
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
	endpoint := a.baseURL
	if !strings.HasSuffix(endpoint, path) {
		endpoint += path
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
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
		var providerError struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    any    `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &providerError) == nil && providerError.Error.Message != "" {
			return fmt.Errorf("AI provider returned %s: %s (%v)", response.Status, providerError.Error.Message, providerError.Error.Code)
		}
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
	content := []map[string]any{{"type": "input_text", "text": request.Prompt}}
	for _, attachment := range request.Attachments {
		if strings.HasPrefix(attachment.MediaType, "image/") {
			content = append(content, map[string]any{"type": "input_image", "image_url": attachment.DataURL, "detail": "auto"})
		} else {
			content = append(content, map[string]any{"type": "input_file", "filename": attachment.Name, "file_data": attachment.DataURL})
		}
	}
	input := []map[string]any{{"role": "user", "content": content}}
	err := a.post(ctx, "/responses", map[string]any{"model": a.model, "instructions": request.System, "input": input, "store": false}, &response)
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
	content := []map[string]any{{"type": "text", "text": request.Prompt}}
	for _, attachment := range request.Attachments {
		if strings.HasPrefix(attachment.MediaType, "image/") {
			content = append(content, map[string]any{"type": "image_url", "image_url": map[string]any{"url": attachment.DataURL, "detail": "auto"}})
			continue
		}
		text, err := decodeTextAttachment(attachment)
		if err != nil {
			return "", fmt.Errorf("Chat adapter attachment %q: %w; use the Responses adapter for PDFs and binary files", attachment.Name, err)
		}
		content = append(content, map[string]any{"type": "text", "text": fmt.Sprintf("Attached file %s:\n%s", attachment.Name, text)})
	}
	payload := map[string]any{"model": a.model, "messages": []map[string]any{{"role": "system", "content": request.System}, {"role": "user", "content": content}}}
	if err := a.post(ctx, "/chat/completions", payload, &response); err != nil {
		return "", err
	}
	if len(response.Choices) == 0 || response.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("Chat Completions API returned no text")
	}
	return response.Choices[0].Message.Content, nil
}

func decodeTextAttachment(attachment Attachment) (string, error) {
	comma := strings.IndexByte(attachment.DataURL, ',')
	if comma < 0 || !strings.Contains(attachment.DataURL[:comma], ";base64") {
		return "", fmt.Errorf("invalid data URL")
	}
	data, err := base64.StdEncoding.DecodeString(attachment.DataURL[comma+1:])
	if err != nil {
		return "", fmt.Errorf("decode data: %w", err)
	}
	if !strings.HasPrefix(attachment.MediaType, "text/") && attachment.MediaType != "application/json" && attachment.MediaType != "application/xml" {
		return "", fmt.Errorf("unsupported media type %s", attachment.MediaType)
	}
	return string(data), nil
}
