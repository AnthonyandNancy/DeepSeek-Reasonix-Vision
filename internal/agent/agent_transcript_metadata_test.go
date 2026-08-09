package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
	"reasonix/internal/vision"
)

type transcriptMetadataTool struct {
	err    error
	images []string
}

func (*transcriptMetadataTool) Name() string { return "transcript_metadata" }
func (*transcriptMetadataTool) Description() string {
	return "returns provider-excluded transcript metadata"
}
func (*transcriptMetadataTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (*transcriptMetadataTool) ReadOnly() bool          { return true }
func (*transcriptMetadataTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "fallback execute path", nil
}
func (t *transcriptMetadataTool) ExecuteWithTranscriptMetadata(context.Context, json.RawMessage) (tool.TranscriptMetadataResult, error) {
	return tool.TranscriptMetadataResult{
		Output: "visual evidence", Images: append([]string(nil), t.images...),
		VisualAnalyses: []provider.VisualAnalysisRecord{{
			ID: "vision-tool", Initiator: "main_model_tool", Status: "ready", Summary: "settings dialog",
		}},
	}, t.err
}

func TestAgentMergesToolTranscriptMetadataWithToolImageBridge(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(&transcriptMetadataTool{images: []string{bridgeShotDataURL}})
	processor := &recordingToolImageProcessor{out: vision.ToolImageOutput{
		Text: "bridged visual evidence", Success: true,
		VisualAnalyses: []provider.VisualAnalysisRecord{{
			ID: "vision-bridge", Initiator: vision.AnalysisInitiatorToolMediaBridge, Status: "ready",
		}},
	}}
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{toolCallChunk("call-1", "transcript_metadata", `{}`), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}
	sess := NewSession("sys")
	a := New(prov, reg, sess, Options{ToolImages: processor}, event.Discard)
	if err := a.Run(context.Background(), "reanalyze"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	msg := transcriptMetadataToolMessage(sess)
	if msg == nil || msg.Content != "bridged visual evidence" || len(msg.VisualAnalyses) != 2 {
		t.Fatalf("merged tool result = %+v", msg)
	}
	if msg.VisualAnalyses[0].ID != "vision-tool" || msg.VisualAnalyses[1].ID != "vision-bridge" {
		t.Fatalf("visual analysis merge order = %+v", msg.VisualAnalyses)
	}
}

func TestAgentPersistsToolTranscriptVisualMetadata(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(&transcriptMetadataTool{})
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{toolCallChunk("call-1", "transcript_metadata", `{}`), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}
	sess := NewSession("sys")
	a := New(prov, reg, sess, Options{}, event.Discard)
	if err := a.Run(context.Background(), "reanalyze"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	msg := transcriptMetadataToolMessage(sess)
	if msg == nil || msg.Content != "visual evidence" || len(msg.VisualAnalyses) != 1 {
		t.Fatalf("tool result = %+v", msg)
	}
	if record := msg.VisualAnalyses[0]; record.ID != "vision-tool" || record.Initiator != "main_model_tool" || record.Summary != "settings dialog" {
		t.Fatalf("visual analysis = %+v", record)
	}
	if len(prov.requests) < 2 {
		t.Fatalf("provider requests = %d, want continuation request", len(prov.requests))
	}
	for _, requestMessage := range prov.requests[1].Messages {
		if requestMessage.Role == provider.RoleTool && len(requestMessage.VisualAnalyses) != 0 {
			t.Fatalf("provider request leaked transcript metadata: %+v", requestMessage)
		}
	}
}

func TestAgentPersistsToolTranscriptVisualMetadataOnError(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(&transcriptMetadataTool{err: errors.New("vision failed")})
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{toolCallChunk("call-1", "transcript_metadata", `{}`), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "handled"}, {Type: provider.ChunkDone}},
	}}
	sess := NewSession("sys")
	a := New(prov, reg, sess, Options{}, event.Discard)
	if err := a.Run(context.Background(), "reanalyze"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	msg := transcriptMetadataToolMessage(sess)
	if msg == nil || len(msg.VisualAnalyses) != 1 || msg.VisualAnalyses[0].ID != "vision-tool" {
		t.Fatalf("failed tool result lost visual metadata: %+v", msg)
	}
}

func transcriptMetadataToolMessage(sess *Session) *provider.Message {
	for i := range sess.Messages {
		if sess.Messages[i].Role == provider.RoleTool && sess.Messages[i].Name == "transcript_metadata" {
			return &sess.Messages[i]
		}
	}
	return nil
}
