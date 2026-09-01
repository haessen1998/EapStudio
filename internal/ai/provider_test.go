package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResponsesAdapter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/responses" || request.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("unexpected request %s, auth=%q", request.URL.Path, request.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"output_text": "equipment selected"})
	}))
	defer server.Close()
	provider, err := NewProvider(Config{Provider: "responses", BaseURL: server.URL, Model: "test-model"}, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	answer, err := provider.Complete(context.Background(), Request{System: "grounded", Prompt: "state"})
	if err != nil || answer != "equipment selected" {
		t.Fatalf("answer=%q err=%v", answer, err)
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
