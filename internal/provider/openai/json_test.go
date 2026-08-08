package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"reasonix/internal/provider"
)

func TestNonStreamingJSONChatResponseNormalizesTextUsageAndImageInput(t *testing.T) {
	var requestBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chat_json","choices":[{"message":{"role":"assistant","content":"json answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`)
	}))
	defer srv.Close()
	p, err := New(provider.Config{Name: "agnes", APIKey: "key", BaseURL: srv.URL, Model: "agnes-2.0-flash", Extra: map[string]any{"vision": true}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stream := false
	ch, err := p.Stream(context.Background(), provider.Request{Stream: &stream, Messages: []provider.Message{{Role: provider.RoleUser, Content: "describe", Images: []string{"data:image/png;base64,AAAA"}}}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var chunks []provider.Chunk
	for chunk := range ch {
		chunks = append(chunks, chunk)
	}
	if got, _ := requestBody["stream"].(bool); got {
		t.Fatalf("request stream = true, want false: %#v", requestBody)
	}
	parts := requestBody["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(parts) != 2 || parts[1].(map[string]any)["type"] != "image_url" || parts[1].(map[string]any)["image_url"].(map[string]any)["url"] != "data:image/png;base64,AAAA" {
		t.Fatalf("image input = %#v, want image_url with the data URL", parts)
	}
	var text string
	var usage *provider.Usage
	for _, chunk := range chunks {
		switch chunk.Type {
		case provider.ChunkText:
			text += chunk.Text
		case provider.ChunkUsage:
			usage = chunk.Usage
		case provider.ChunkError:
			t.Fatalf("unexpected chunk error: %v", chunk.Err)
		}
	}
	if text != "json answer" || usage == nil || usage.PromptTokens != 7 || usage.CompletionTokens != 3 || usage.TotalTokens != 10 || usage.FinishReason != "stop" {
		t.Fatalf("text=%q usage=%+v, want normalized completed response", text, usage)
	}
	if chunks[len(chunks)-1].Type != provider.ChunkDone {
		t.Fatalf("last chunk = %v, want ChunkDone", chunks[len(chunks)-1].Type)
	}
}

func TestNonStreamingChatToleratesForcedSSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"sse answer\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":2,\"total_tokens\":4}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	stream := false
	p, err := New(provider.Config{Name: "agnes", APIKey: "key", BaseURL: srv.URL, Model: "m", Extra: map[string]any{"vision": true}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ch, err := p.Stream(context.Background(), provider.Request{Stream: &stream, Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var text string
	var chunks []provider.Chunk
	for chunk := range ch {
		chunks = append(chunks, chunk)
		if chunk.Type == provider.ChunkText {
			text += chunk.Text
		}
		if chunk.Type == provider.ChunkError {
			t.Fatalf("unexpected chunk error: %v", chunk.Err)
		}
	}
	if text != "sse answer" || chunks[len(chunks)-1].Type != provider.ChunkDone {
		t.Fatalf("chunks = %#v, want SSE answer followed by done", chunks)
	}
}

func TestNonStreamingJSONChatPreservesReasoningAndFunctionCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chat_json","choices":[{"message":{"role":"assistant","reasoning_content":"think","content":"answer","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`)
	}))
	defer srv.Close()
	stream := false
	p, err := New(provider.Config{Name: "agnes", APIKey: "key", BaseURL: srv.URL, Model: "m"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ch, err := p.Stream(context.Background(), provider.Request{Stream: &stream, Messages: []provider.Message{{Role: provider.RoleUser, Content: "lookup"}}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var reasoning, text string
	var call *provider.ToolCall
	for chunk := range ch {
		switch chunk.Type {
		case provider.ChunkReasoning:
			reasoning += chunk.Text
		case provider.ChunkText:
			text += chunk.Text
		case provider.ChunkToolCall:
			call = chunk.ToolCall
		case provider.ChunkError:
			t.Fatalf("unexpected chunk error: %v", chunk.Err)
		}
	}
	if reasoning != "think" || text != "answer" {
		t.Fatalf("reasoning=%q text=%q", reasoning, text)
	}
	if call == nil || call.ID != "call_1" || call.Name != "lookup" || call.Arguments != "{}" {
		t.Fatalf("function call = %+v", call)
	}
}
