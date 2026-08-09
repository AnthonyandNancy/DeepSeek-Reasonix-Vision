package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
	"reasonix/internal/vision"
)

type recordingToolImageProcessor struct {
	mu    sync.Mutex
	calls []vision.ToolImageInput
	out   vision.ToolImageOutput
}

func (f *recordingToolImageProcessor) ProcessToolImages(_ context.Context, in vision.ToolImageInput) vision.ToolImageOutput {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, in)
	return f.out
}

func (f *recordingToolImageProcessor) inputs() []vision.ToolImageInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]vision.ToolImageInput(nil), f.calls...)
}

const bridgeShotDataURL = "data:image/png;base64,QUFB"

func bridgeToolMessageImages(s *Session, name string) []string {
	for i := range s.Messages {
		if s.Messages[i].Role == provider.RoleTool && s.Messages[i].Name == name {
			return s.Messages[i].Images
		}
	}
	return nil
}

func bridgeToolMessageLocalImages(s *Session, name string) []string {
	for i := range s.Messages {
		if s.Messages[i].LocalOnly && s.Messages[i].Name == provider.LocalOnlyToolName && len(s.Messages[i].Images) > 0 {
			return s.Messages[i].Images
		}
	}
	return nil
}

func TestAgentToolImagesTextModelStoresVisualEvidenceNotRawImage(t *testing.T) {
	fp := &recordingToolImageProcessor{out: vision.ToolImageOutput{
		Text:    "read shot.png\n\n<visual-evidence schema=\"modlens-v2\">\nDIRECT_EVIDENCE:\nOCR: 错误码 500\n</visual-evidence>",
		Success: true,
	}}
	reg := tool.NewRegistry()
	reg.Add(&fakeImageTool{text: "read shot.png", images: []string{bridgeShotDataURL}})
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{toolCallChunk("c1", "shot", `{}`), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}
	sess := NewSession("sys")
	a := New(prov, reg, sess, Options{ToolImages: fp, ModelRef: "text/main"}, event.Discard)
	if err := a.Run(context.Background(), "look at the screenshot"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if img := bridgeToolMessageImages(sess, "shot"); len(img) != 0 {
		t.Fatalf("text model retained raw tool image: %v", img)
	}
	if img := bridgeToolMessageLocalImages(sess, "shot"); len(img) != 1 || img[0] != bridgeShotDataURL {
		t.Fatalf("text model local tool images = %v, want recoverable original image", img)
	}
	if content := lastToolResult(sess, "shot"); !strings.Contains(content, `schema="modlens-v2"`) || !strings.Contains(content, "错误码 500") {
		t.Fatalf("tool message missing structured visual evidence:\n%s", content)
	}
	in := fp.inputs()
	if len(in) != 1 || in[0].ToolName != "shot" || in[0].ToolCallID != "c1" || in[0].ModelRef != "text/main" {
		t.Fatalf("processor input = %+v", in)
	}
	if in[0].ModelSupportsImages {
		t.Fatal("text model incorrectly marked image-capable")
	}
	if !strings.Contains(in[0].TaskContext, "look at the screenshot") {
		t.Fatalf("task context missing raw user focus: %q", in[0].TaskContext)
	}
}

func TestAgentToolImagesVisionModelStillUsesConfiguredVisualProcessor(t *testing.T) {
	fp := &recordingToolImageProcessor{out: vision.ToolImageOutput{Text: "visual evidence", Success: true}}
	reg := tool.NewRegistry()
	reg.Add(&fakeImageTool{text: "shot", images: []string{bridgeShotDataURL}})
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{toolCallChunk("c1", "shot", `{}`), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}
	sess := NewSession("sys")
	a := New(prov, reg, sess, Options{ToolImages: fp, ModelSupportsImages: true}, event.Discard)
	if err := a.Run(context.Background(), "look"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if img := bridgeToolMessageImages(sess, "shot"); len(img) != 0 {
		t.Fatalf("vision model tool images = %v, want visual evidence without raw image", img)
	}
	if img := bridgeToolMessageLocalImages(sess, "shot"); len(img) != 1 || img[0] != bridgeShotDataURL {
		t.Fatalf("vision model local tool images = %v, want recoverable original image", img)
	}
	if in := fp.inputs(); len(in) != 1 || !in[0].ModelSupportsImages {
		t.Fatalf("processor input = %+v, want configured processor to receive image-capable=true", in)
	}
	if content := lastToolResult(sess, "shot"); content != "visual evidence" {
		t.Fatalf("tool message content = %q, want visual evidence", content)
	}
}

func TestAgentToolImageProcessorSkipsTextOnlyResults(t *testing.T) {
	fp := &recordingToolImageProcessor{}
	reg := tool.NewRegistry()
	reg.Add(&fakeImageTool{text: "plain text"})
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{toolCallChunk("c1", "shot", `{}`), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}
	sess := NewSession("sys")
	a := New(prov, reg, sess, Options{ToolImages: fp}, event.Discard)
	if err := a.Run(context.Background(), "look"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if in := fp.inputs(); len(in) != 0 {
		t.Fatalf("processor called for image-less result: %+v", in)
	}
}

func TestCurrentTaskContextPrefersRawUserContentForVisionBridge(t *testing.T) {
	sess := NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "<host-context>private host context</host-context>\nuser question", RawContent: "user question"})
	a := &Agent{session: sess}
	if got := a.currentTaskContext(); got != "user question" {
		t.Fatalf("task context = %q, want raw user content", got)
	}
}
