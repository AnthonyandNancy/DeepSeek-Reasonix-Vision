package vision

import (
	"context"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

func TestProviderDescriberDefaultTimeoutCoversSlowVisionEndpoints(t *testing.T) {
	if defaultTimeout < 3*time.Minute {
		t.Fatalf("default vision timeout = %s, want at least 3m for slow multimodal gateways", defaultTimeout)
	}
}

type captureProvider struct {
	req    provider.Request
	chunks []provider.Chunk
}

func (p *captureProvider) Name() string { return "capture" }
func (p *captureProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.req = req
	ch := make(chan provider.Chunk, len(p.chunks))
	for _, c := range p.chunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}

func validEvidenceJSON() string {
	return `{"summary":"UI screenshot","ocr":{"full_text":"Save","lines":[{"text":"Save"}]},"layout":{"regions":[{"type":"form","reading_order":1,"text":"Save"}]},"semantics":{"scene":"web interface","entities":[{"name":"Save","type":"button","evidence":"OCR: Save"}],"relations":[]},"uncertainty":["implementation cause is not visible"]}`
}

func TestProviderDescriberRequestsModLensV2JSONWithoutTools(t *testing.T) {
	p := &captureProvider{chunks: []provider.Chunk{{Type: provider.ChunkText, Text: validEvidenceJSON()}}}
	d := NewProviderDescriber(p, nil, nil)
	got, _, err := d.DescribeOnce(context.Background(), "p/vision", []Image{{Ref: "a.png", DataURL: "data:image/png;base64,AA=="}}, "why is the dialog clipped?")
	if err != nil {
		t.Fatalf("DescribeOnce: %v", err)
	}
	if got.Summary != "UI screenshot" {
		t.Fatalf("got=%+v", got)
	}
	if len(p.req.Tools) != 0 {
		t.Fatalf("vision request unexpectedly exposed tools: %+v", p.req.Tools)
	}
	if p.req.ResponseFormat == nil || p.req.ResponseFormat.Type != "json_object" {
		t.Fatalf("response format=%+v", p.req.ResponseFormat)
	}
	if p.req.Stream == nil || *p.req.Stream {
		t.Fatalf("stream=%v, want explicit non-streaming vision request", p.req.Stream)
	}
	if len(p.req.Messages) != 2 || len(p.req.Messages[1].Images) != 1 {
		t.Fatalf("messages=%+v", p.req.Messages)
	}
	if !strings.Contains(p.req.Messages[0].Content, "ModLens Output Schema v2") {
		t.Fatalf("system prompt missing schema: %s", p.req.Messages[0].Content)
	}
	if !strings.Contains(p.req.Messages[0].Content, "bbox") || !strings.Contains(p.req.Messages[0].Content, "confidence") {
		t.Fatalf("system prompt missing v2 prohibition")
	}
	if !strings.Contains(p.req.Messages[1].Content, "why is the dialog clipped?") {
		t.Fatalf("focus missing: %s", p.req.Messages[1].Content)
	}
}

func TestProviderDescriberRejectsSchemaMismatch(t *testing.T) {
	p := &captureProvider{chunks: []provider.Chunk{{Type: provider.ChunkText, Text: `{"description":"looks like a page"}`}}}
	d := NewProviderDescriber(p, nil, nil)
	if _, _, err := d.DescribeOnce(context.Background(), "p/vision", []Image{{DataURL: "data:image/png;base64,AA=="}}, ""); err == nil {
		t.Fatal("expected schema error")
	}
}

func TestProviderDescriberRejectsToolCall(t *testing.T) {
	p := &captureProvider{chunks: []provider.Chunk{{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{Name: "bash"}}}}
	d := NewProviderDescriber(p, nil, nil)
	if _, _, err := d.DescribeOnce(context.Background(), "p/vision", []Image{{DataURL: "data:image/png;base64,AA=="}}, ""); err != ErrUnexpectedVisionToolCall {
		t.Fatalf("err=%v", err)
	}
}

func TestProviderDescriberEmitsVisionLifecycleAndSafeDeltas(t *testing.T) {
	p := &captureProvider{chunks: []provider.Chunk{
		{Type: provider.ChunkReasoning, Text: "checking pixels"},
		{Type: provider.ChunkText, Text: validEvidenceJSON()},
	}}
	var events []event.Event
	sink := event.FuncSink(func(e event.Event) { events = append(events, e) })
	d := NewProviderDescriber(p, nil, sink)
	if _, _, err := d.DescribeOnce(context.Background(), "p/vision", []Image{{DataURL: "data:image/png;base64,AA=="}}, ""); err != nil {
		t.Fatalf("DescribeOnce: %v", err)
	}
	var stages []event.VisionProgressStage
	var response, reasoning string
	for _, e := range events {
		if e.Kind != event.VisionProgress || e.VisionProgress == nil {
			continue
		}
		stages = append(stages, e.VisionProgress.Stage)
		response += e.VisionProgress.ResponseDelta
		reasoning += e.VisionProgress.ReasoningDelta
	}
	if len(stages) < 5 || stages[0] != event.VisionStagePreparing || stages[len(stages)-1] != event.VisionStageReady {
		t.Fatalf("vision stages = %v", stages)
	}
	if response != validEvidenceJSON() || reasoning != "checking pixels" {
		t.Fatalf("response=%q reasoning=%q", response, reasoning)
	}
}
