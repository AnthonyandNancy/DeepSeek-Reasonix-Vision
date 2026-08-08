package responses

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"reasonix/internal/provider"
)

func streamRequested(req provider.Request) bool { return req.Stream == nil || *req.Stream }

func isEventStream(resp *http.Response) bool {
	return resp != nil && strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
}

func usageFromResponse(response *sseResponse) *provider.Usage {
	if response == nil || response.Usage == nil {
		return &provider.Usage{}
	}
	u := response.Usage
	cached, reasoning := 0, 0
	if u.InputTokensDetails != nil {
		cached = u.InputTokensDetails.CachedTokens
	}
	if u.OutputTokensDetails != nil {
		reasoning = u.OutputTokensDetails.ReasoningTokens
	}
	miss := u.InputTokens - cached
	if miss < 0 {
		miss = 0
	}
	total := u.TotalTokens
	if total == 0 {
		total = u.InputTokens + u.OutputTokens
	}
	return &provider.Usage{PromptTokens: u.InputTokens, CompletionTokens: u.OutputTokens, TotalTokens: total, CacheHitTokens: cached, CacheMissTokens: miss, ReasoningTokens: reasoning}
}

func authErrorFromResponse(c *client, responseError *sseError) error {
	if responseError == nil {
		return nil
	}
	value := strings.ToLower(responseError.Code + " " + responseError.Message)
	if !strings.Contains(value, "auth") && !strings.Contains(value, "api key") && !strings.Contains(value, "unauthorized") && !strings.Contains(value, "forbidden") && !strings.Contains(value, "permission") {
		return nil
	}
	status := http.StatusUnauthorized
	if strings.Contains(value, "forbidden") || strings.Contains(value, "permission") {
		status = http.StatusForbidden
	}
	return &provider.AuthError{Provider: c.name, KeyEnv: c.keyEnv, KeySource: c.keySource, Status: status, HasKey: c.apiKey != "", Body: responseError.Message}
}

func (c *client) readJSONResponse(ctx context.Context, resp *http.Response, out chan<- provider.Chunk, requestMessages []provider.Message) {
	defer resp.Body.Close()
	defer close(out)
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		_ = sendChunk(ctx, out, provider.Chunk{Type: provider.ChunkError, Err: err})
		return
	}
	var response jsonResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		_ = sendChunk(ctx, out, provider.Chunk{Type: provider.ChunkError, Err: provider.StreamDecodeError(c.name, string(raw), err)})
		return
	}
	if response.Error != nil {
		err := fmt.Errorf("responses: %s", response.Error.Message)
		if authErr := authErrorFromResponse(c, response.Error); authErr != nil {
			err = authErr
		}
		_ = sendChunk(ctx, out, provider.Chunk{Type: provider.ChunkError, Err: err})
		return
	}
	if strings.EqualFold(response.Status, "failed") {
		_ = sendChunk(ctx, out, provider.Chunk{Type: provider.ChunkError, Err: errors.New("responses: response failed")})
		return
	}

	var text, reasoning strings.Builder
	var reasoningID, reasoningStatus string
	var toolCalls []provider.ToolCall
	for _, item := range response.Output {
		switch item.Type {
		case "message", "output_text":
			for _, part := range item.Content {
				if part.Text == "" {
					continue
				}
				text.WriteString(part.Text)
				if !sendChunk(ctx, out, provider.Chunk{Type: provider.ChunkText, Text: part.Text}) {
					return
				}
			}
			if item.Text != "" {
				text.WriteString(item.Text)
				if !sendChunk(ctx, out, provider.Chunk{Type: provider.ChunkText, Text: item.Text}) {
					return
				}
			}
		case "reasoning":
			reasoningID, reasoningStatus = item.ID, item.Status
			parts := item.Summary
			if len(parts) == 0 {
				parts = item.Content
			}
			for _, part := range parts {
				if part.Text == "" {
					continue
				}
				reasoning.WriteString(part.Text)
				if !sendChunk(ctx, out, provider.Chunk{Type: provider.ChunkReasoning, Text: part.Text}) {
					return
				}
			}
		case "function_call":
			callID := item.CallID
			if callID == "" {
				callID = item.ID
			}
			call := provider.ToolCall{ID: callID, Name: item.Name, Arguments: item.Arguments}
			if !sendChunk(ctx, out, provider.Chunk{Type: provider.ChunkToolCallStart, ToolCall: &provider.ToolCall{ID: call.ID, Name: call.Name}}) ||
				!sendChunk(ctx, out, provider.Chunk{Type: provider.ChunkToolCall, ToolCall: &call}) {
				return
			}
			toolCalls = append(toolCalls, call)
		}
	}

	if response.Usage != nil {
		usage := usageFromResponse(&sseResponse{Usage: response.Usage})
		if strings.EqualFold(response.Status, "incomplete") {
			switch response.IncompleteDetails.Reason {
			case "max_output_tokens":
				usage.FinishReason = "length"
			case "content_filter":
				usage.FinishReason = "content_filter"
			default:
				usage.FinishReason = "incomplete"
			}
		} else {
			usage.FinishReason = "stop"
		}
		provider.ApplyRequestAttemptCount(ctx, usage)
		if !sendChunk(ctx, out, provider.Chunk{Type: provider.ChunkUsage, Usage: usage}) {
			return
		}
	}
	if response.ID != "" && !strings.EqualFold(response.Status, "incomplete") {
		assistant := provider.Message{Role: provider.RoleAssistant, Content: text.String(), ReasoningContent: reasoning.String(), ReasoningID: reasoningID, ReasoningStatus: reasoningStatus, ToolCalls: toolCalls}
		expected := append(append([]provider.Message(nil), requestMessages...), assistant)
		c.mu.Lock()
		c.lastResponseID = response.ID
		c.expectedPrefixDigest = c.conversationDigest(expected)
		c.mu.Unlock()
	}
	if reasoningID != "" || reasoningStatus != "" {
		if !sendChunk(ctx, out, provider.Chunk{Type: provider.ChunkReasoning, ReasoningID: reasoningID, ReasoningStatus: reasoningStatus}) {
			return
		}
	}
	_ = sendChunk(ctx, out, provider.Chunk{Type: provider.ChunkDone})
}

type jsonResponse struct {
	ID                string             `json:"id"`
	Status            string             `json:"status"`
	Output            []jsonResponseItem `json:"output"`
	Usage             *sseUsage          `json:"usage"`
	Error             *sseError          `json:"error"`
	IncompleteDetails incompleteDetails  `json:"incomplete_details"`
}

type jsonResponseItem struct {
	ID        string             `json:"id"`
	Type      string             `json:"type"`
	Status    string             `json:"status"`
	CallID    string             `json:"call_id"`
	Name      string             `json:"name"`
	Arguments string             `json:"arguments"`
	Text      string             `json:"text"`
	Content   []responseTextPart `json:"content"`
	Summary   []responseTextPart `json:"summary"`
}

type responseTextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
