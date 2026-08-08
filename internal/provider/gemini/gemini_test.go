package gemini

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
)

func collectGemini(t *testing.T, p provider.Provider, req provider.Request) []provider.Chunk {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := p.Stream(ctx, req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var chunks []provider.Chunk
	for chunk := range stream {
		chunks = append(chunks, chunk)
	}
	return chunks
}

func TestGenerateContentBuildsOfficialMultimodalRequest(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/vision-model:generateContent" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("x-goog-api-key"); got != "key" {
			t.Fatalf("x-goog-api-key = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"{\"summary\":\"ok\"}"}]}}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3,"totalTokenCount":10}}`)
	}))
	defer server.Close()

	p, err := New(provider.Config{Name: "gemini", APIKey: "key", BaseURL: server.URL + "/v1beta", Model: "vision-model"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stream := false
	chunks := collectGemini(t, p, provider.Request{
		Stream: &stream,
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: "system"},
			{Role: provider.RoleUser, Content: "describe", Images: []string{"data:image/png;base64,AAAA"}},
		},
		ResponseFormat: &provider.ResponseFormat{Type: "json_object"},
	})

	if _, exists := requestBody["stream"]; exists {
		t.Fatalf("non-streaming Gemini request has stream field: %#v", requestBody["stream"])
	}
	if got := requestBody["systemInstruction"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"]; got != "system" {
		t.Fatalf("systemInstruction = %#v", requestBody["systemInstruction"])
	}
	contents := requestBody["contents"].([]any)
	parts := contents[0].(map[string]any)["parts"].([]any)
	if parts[0].(map[string]any)["text"] != "describe" {
		t.Fatalf("text part = %#v", parts[0])
	}
	image := parts[1].(map[string]any)["inlineData"].(map[string]any)
	if image["mimeType"] != "image/png" || image["data"] != "AAAA" {
		t.Fatalf("inlineData = %#v", image)
	}
	config := requestBody["generationConfig"].(map[string]any)
	if config["responseMimeType"] != "application/json" {
		t.Fatalf("generationConfig = %#v", config)
	}
	if len(chunks) == 0 || chunks[len(chunks)-1].Type != provider.ChunkDone {
		t.Fatalf("chunks = %#v", chunks)
	}
}

func TestGenerateContentDisablesThinkingWithExplicitZeroBudget(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`)
	}))
	defer server.Close()

	p, err := New(provider.Config{
		Name: "gemini", APIKey: "key", BaseURL: server.URL + "/v1beta", Model: "vision-model",
		Extra: map[string]any{"effort": "disabled"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stream := false
	chunks := collectGemini(t, p, provider.Request{
		Stream:   &stream,
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})
	if len(chunks) == 0 || chunks[len(chunks)-1].Type != provider.ChunkDone {
		t.Fatalf("chunks = %#v", chunks)
	}
	generationConfig := requestBody["generationConfig"].(map[string]any)
	thinkingConfig := generationConfig["thinkingConfig"].(map[string]any)
	if got, ok := thinkingConfig["thinkingBudget"].(float64); !ok || got != 0 {
		t.Fatalf("thinkingConfig = %#v, want explicit thinkingBudget 0", thinkingConfig)
	}
}

func TestGenerateContentPlacesThoughtSignatureOnPart(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`)
	}))
	defer server.Close()

	p, err := New(provider.Config{Name: "gemini", APIKey: "key", BaseURL: server.URL + "/v1beta", Model: "vision-model"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stream := false
	_ = collectGemini(t, p, provider.Request{
		Stream: &stream,
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: "use the tool"},
			{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{
				Name: "read_file", Arguments: `{"path":"README.md"}`, ThoughtSignature: "sig-123",
			}}},
		},
	})
	contents := requestBody["contents"].([]any)
	parts := contents[1].(map[string]any)["parts"].([]any)
	toolPart := parts[0].(map[string]any)
	if got := toolPart["thoughtSignature"]; got != "sig-123" {
		t.Fatalf("part thoughtSignature = %#v, want sig-123", got)
	}
	functionCall := toolPart["functionCall"].(map[string]any)
	if _, exists := functionCall["thoughtSignature"]; exists {
		t.Fatalf("functionCall contains misplaced thoughtSignature: %#v", functionCall)
	}
}

func TestGenerateContentStreamSeparatesThoughtPartsAndText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"think\",\"thought\":true}]}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"answer\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":1,\"candidatesTokenCount\":2,\"totalTokenCount\":3}}\n\n")
	}))
	defer server.Close()

	p, err := New(provider.Config{Name: "gemini", APIKey: "key", BaseURL: server.URL + "/v1beta", Model: "m"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	chunks := collectGemini(t, p, provider.Request{Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}})
	var reasoning, text string
	for _, chunk := range chunks {
		switch chunk.Type {
		case provider.ChunkReasoning:
			reasoning += chunk.Text
		case provider.ChunkText:
			text += chunk.Text
		case provider.ChunkError:
			t.Fatalf("unexpected error: %v", chunk.Err)
		}
	}
	if reasoning != "think" || text != "answer" {
		t.Fatalf("reasoning=%q text=%q chunks=%#v", reasoning, text, chunks)
	}
	if chunks[len(chunks)-1].Type != provider.ChunkDone {
		t.Fatalf("last chunk = %#v", chunks[len(chunks)-1])
	}
	var usage *provider.Usage
	for _, chunk := range chunks {
		if chunk.Type == provider.ChunkUsage {
			usage = chunk.Usage
		}
	}
	if usage == nil || usage.PromptTokens != 1 || usage.CompletionTokens != 2 || usage.TotalTokens != 3 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestGenerateContentStreamUsesSSEEndpoint(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.RequestURI()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n")
	}))
	defer server.Close()
	p, err := New(provider.Config{Name: "gemini", APIKey: "key", BaseURL: server.URL + "/v1beta", Model: "m"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	collectGemini(t, p, provider.Request{Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}})
	if !strings.Contains(path, ":streamGenerateContent") || !strings.Contains(path, "alt=sse") {
		t.Fatalf("stream path = %q", path)
	}
}

func TestGenerateContentRejectsMalformedImageBeforeNetwork(t *testing.T) {
	p, err := New(provider.Config{Name: "gemini", APIKey: "key", BaseURL: "http://127.0.0.1:1/v1beta", Model: "m"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = p.Stream(context.Background(), provider.Request{Messages: []provider.Message{{
		Role: provider.RoleUser, Content: "describe", Images: []string{"not-an-image"},
	}}})
	if err == nil || !strings.Contains(err.Error(), "image") {
		t.Fatalf("error = %v, want a local image validation error", err)
	}
}
