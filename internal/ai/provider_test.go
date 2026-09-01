package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponsesAdapter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/responses" || request.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("unexpected request %s, auth=%q", request.URL.Path, request.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(request.Body)
		if !strings.Contains(string(body), `"type":"input_image"`) || !strings.Contains(string(body), `"store":false`) {
			t.Fatalf("unexpected Responses payload: %s", body)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"output": []any{map[string]any{"content": []any{map[string]any{"type": "output_text", "text": "equipment selected"}}}}})
	}))
	defer server.Close()
	provider, err := NewProvider(Config{Provider: "responses", BaseURL: server.URL, Model: "test-model"}, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	answer, err := provider.Complete(context.Background(), Request{System: "grounded", Prompt: "state", Attachments: []Attachment{{Name: "trace.png", MediaType: "image/png", DataURL: "data:image/png;base64,AA==", Size: 1}}})
	if err != nil || answer != "equipment selected" {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
}

func TestNormalizeOpenAIBaseURLAndFullEndpoint(t *testing.T) {
	if got := normalizeBaseURL("https://api.openai.com"); got != "https://api.openai.com/v1" {
		t.Fatalf("base URL = %q", got)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/responses" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"output_text": "ok"})
	}))
	defer server.Close()
	provider, err := NewProvider(Config{Provider: "responses", BaseURL: server.URL + "/v1/responses", Model: "test"}, "key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Complete(context.Background(), Request{Prompt: "test"}); err != nil {
		t.Fatal(err)
	}
}

func TestChatCompletionsAdapter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "S6F11 received"}}}})
	}))
	defer server.Close()
	provider, err := NewProvider(Config{Provider: "chat", BaseURL: server.URL, Model: "test-model"}, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	answer, err := provider.Complete(context.Background(), Request{System: "grounded", Prompt: "latest"})
	if err != nil || answer != "S6F11 received" {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
}
