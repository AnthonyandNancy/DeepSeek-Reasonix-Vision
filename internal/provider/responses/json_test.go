package responses

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"reasonix/internal/provider"
)

func TestNonStreamingJSONResponseNormalizesTextUsageAndImageInput(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_json","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"json answer"}]}],"usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10}}`)
	}))
	defer server.Close()

	p := New(Config{Name: "agnes", APIKey: "key", BaseURL: server.URL, Model: "agnes-2.0-flash", Mode: "stateless", Extra: map[string]any{"vision": true}})
	stream := false
	chunks := collect(t, p, provider.Request{Stream: &stream, Messages: []provider.Message{{Role: provider.RoleUser, Content: "describe", Images: []string{"data:image/png;base64,AAAA"}}}})
	if _, exists := requestBody["stream"]; exists {
		t.Fatalf("non-streaming Responses request must omit stream: %#v", requestBody)
	}
	parts := requestBody["input"].([]any)[0].(map[string]any)["content"].([]any)
	if len(parts) != 2 || parts[1].(map[string]any)["type"] != "input_image" || parts[1].(map[string]any)["image_url"] != "data:image/png;base64,AAAA" {
		t.Fatalf("image input = %#v, want input_image with the data URL", parts)
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

func TestNonStreamingResponsesToleratesForcedSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeEvents(w, `{"type":"response.output_text.delta","item_id":"msg_1","content_index":0,"delta":"sse answer"}`, `{"type":"response.completed","response":{"id":"resp_sse","usage":{"input_tokens":2,"output_tokens":2,"total_tokens":4}}}`)
	}))
	defer server.Close()
	stream := false
	chunks := collect(t, New(Config{Name: "agnes", APIKey: "key", BaseURL: server.URL, Model: "m", Mode: "stateless"}), provider.Request{Stream: &stream, Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}})
	var text string
	for _, chunk := range chunks {
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

func TestVisionResponsesSchemaRejectionDoesNotFallbackToChatCompletions(t *testing.T) {
	var responsesCalls, chatCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/responses":
			responsesCalls++
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"input_image is not supported by this endpoint"}}`)
		case "/chat/completions":
			chatCalls++
			http.Error(w, "unexpected cross-protocol fallback", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	p := New(Config{Name: "responses", APIKey: "key", BaseURL: server.URL, Model: "m", Mode: "stateless", Extra: map[string]any{"vision": true}})
	stream := false
	if _, err := p.Stream(context.Background(), provider.Request{Stream: &stream, Messages: []provider.Message{{Role: provider.RoleUser, Content: "describe", Images: []string{"data:image/png;base64,AAAA"}}}}); err == nil {
		t.Fatal("expected the Responses error to be returned without a Chat fallback")
	}
	if responsesCalls != 1 || chatCalls != 0 {
		t.Fatalf("responses_calls=%d chat_calls=%d, want one Responses request and no Chat request", responsesCalls, chatCalls)
	}
}

func TestNonStreamingJSONResponsePreservesReasoningAndFunctionCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_json","status":"completed","output":[{"type":"reasoning","id":"rs_1","status":"completed","summary":[{"type":"summary_text","text":"think"}]},{"type":"message","content":[{"type":"output_text","text":"answer"}]},{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup","arguments":"{}"}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}`)
	}))
	defer server.Close()
	stream := false
	chunks := collect(t, New(Config{Name: "agnes", APIKey: "key", BaseURL: server.URL, Model: "m", Mode: "stateless"}), provider.Request{Stream: &stream, Messages: []provider.Message{{Role: provider.RoleUser, Content: "lookup"}}})
	var reasoning, text, reasoningID, reasoningStatus string
	var call *provider.ToolCall
	for _, chunk := range chunks {
		switch chunk.Type {
		case provider.ChunkReasoning:
			reasoning += chunk.Text
			reasoningID, reasoningStatus = chunk.ReasoningID, chunk.ReasoningStatus
		case provider.ChunkText:
			text += chunk.Text
		case provider.ChunkToolCall:
			call = chunk.ToolCall
		case provider.ChunkError:
			t.Fatalf("unexpected chunk error: %v", chunk.Err)
		}
	}
	if reasoning != "think" || reasoningID != "rs_1" || reasoningStatus != "completed" || text != "answer" {
		t.Fatalf("reasoning=%q id=%q status=%q text=%q", reasoning, reasoningID, reasoningStatus, text)
	}
	if call == nil || call.ID != "call_1" || call.Name != "lookup" || call.Arguments != "{}" {
		t.Fatalf("function call = %+v", call)
	}
}
