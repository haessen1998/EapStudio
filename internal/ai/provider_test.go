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

func TestProviderAdaptersStreamSSEChunks(t *testing.T) {
	tests := []struct {
		provider string
		path     string
		events   string
		want     string
	}{
		{"responses", "/responses", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"equip\"}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ment\"}\n\ndata: [DONE]\n\n", "equipment"},
		{"chat", "/chat/completions", "data: {\"choices\":[{\"delta\":{\"content\":\"S6F\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"11\"}}]}\n\ndata: [DONE]\n\n", "S6F11"},
	}
	for _, test := range tests {
		t.Run(test.provider, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != test.path {
					t.Fatalf("path = %q", request.URL.Path)
				}
				body, _ := io.ReadAll(request.Body)
				if !strings.Contains(string(body), `"stream":true`) {
					t.Fatalf("payload = %s", body)
				}
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(writer, test.events)
			}))
			defer server.Close()
			provider, err := NewProvider(Config{Provider: test.provider, BaseURL: server.URL, Model: "test"}, "key")
			if err != nil {
				t.Fatal(err)
			}
			var answer strings.Builder
			if err := provider.Stream(context.Background(), Request{Prompt: "latest"}, func(delta string) error {
				answer.WriteString(delta)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if answer.String() != test.want {
				t.Fatalf("answer = %q", answer.String())
			}
		})
	}
}
