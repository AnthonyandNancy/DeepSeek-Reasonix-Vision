package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"reasonix/internal/provider"
)

func (c *client) streamJSON(ctx context.Context, resp *http.Response, out chan<- provider.Chunk) {
	defer resp.Body.Close()
	defer close(out)
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		_ = sendChunk(ctx, out, provider.Chunk{Type: provider.ChunkError, Err: err})
		return
	}
	var response streamResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		_ = sendChunk(ctx, out, provider.Chunk{Type: provider.ChunkError, Err: provider.StreamDecodeError(c.name, string(raw), err)})
		return
	}
	if response.Error != nil {
		_ = sendChunk(ctx, out, provider.Chunk{Type: provider.ChunkError, Err: fmt.Errorf("%s: %s", c.name, response.Error.Message)})
		return
	}
	if len(response.Choices) == 0 {
		if response.Usage != nil {
			u := normaliseUsage(response.Usage)
			provider.ApplyRequestAttemptCount(ctx, u)
			if !sendChunk(ctx, out, provider.Chunk{Type: provider.ChunkUsage, Usage: u}) {
				return
			}
		}
		_ = sendChunk(ctx, out, provider.Chunk{Type: provider.ChunkDone})
		return
	}
	choice := response.Choices[0]
	reasoning := choice.Message.ReasoningContent
	if reasoning == "" {
		reasoning = choice.Message.Reasoning
	}
	if reasoning != "" && !sendChunk(ctx, out, provider.Chunk{Type: provider.ChunkReasoning, Text: reasoning}) {
		return
	}
	if choice.Message.Content != "" && !sendChunk(ctx, out, provider.Chunk{Type: provider.ChunkText, Text: choice.Message.Content}) {
		return
	}
	for index, toolCall := range choice.Message.ToolCalls {
		if toolCall.ID == "" {
			toolCall.ID = fmt.Sprintf("call_%d", index)
		}
		call := provider.ToolCall{ID: toolCall.ID, Name: toolCall.Function.Name, Arguments: toolCall.Function.Arguments}
		if !sendChunk(ctx, out, provider.Chunk{Type: provider.ChunkToolCallStart, ToolCall: &provider.ToolCall{ID: call.ID, Name: call.Name}}) ||
			!sendChunk(ctx, out, provider.Chunk{Type: provider.ChunkToolCall, ToolCall: &call}) {
			return
		}
	}
	if response.Usage != nil {
		u := normaliseUsage(response.Usage)
		if choice.FinishReason != nil {
			u.FinishReason = *choice.FinishReason
		}
		provider.ApplyRequestAttemptCount(ctx, u)
		if !sendChunk(ctx, out, provider.Chunk{Type: provider.ChunkUsage, Usage: u}) {
			return
		}
	}
	_ = sendChunk(ctx, out, provider.Chunk{Type: provider.ChunkDone})
}

func streamRequested(req provider.Request) bool { return req.Stream == nil || *req.Stream }

func isEventStream(resp *http.Response) bool {
	return resp != nil && strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
}

func acceptHeader(stream bool) string {
	if stream {
		return "text/event-stream"
	}
	return "application/json"
}

func emitUsageAndDone(ctx context.Context, out chan<- provider.Chunk, usage *provider.Usage) {
	if usage != nil && !sendChunk(ctx, out, provider.Chunk{Type: provider.ChunkUsage, Usage: usage}) {
		return
	}
	_ = sendChunk(ctx, out, provider.Chunk{Type: provider.ChunkDone})
}

func usageRequestCount(usage *provider.Usage) int {
	if usage != nil && usage.RequestCount > 0 {
		return usage.RequestCount
	}
	return 1
}
