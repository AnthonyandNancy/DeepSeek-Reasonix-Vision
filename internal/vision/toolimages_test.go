package vision

import (
	"context"
	"errors"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

type fakeEvidenceDescriber struct {
	calls    int
	evidence Evidence
	err      error
}

func (f *fakeEvidenceDescriber) DescribeOnce(context.Context, string, []Image, string) (Evidence, *provider.Usage, error) {
	f.calls++
	return f.evidence, nil, f.err
}
func (f *fakeEvidenceDescriber) DescribeToolImagesOnce(context.Context, string, ToolImageDescribeInput) (Evidence, *provider.Usage, error) {
	f.calls++
	return f.evidence, nil, f.err
}

func smallEvidence() Evidence {
	return Evidence{Summary: "button clipped", OCR: OCR{Lines: []OCRLine{{Text: "Save"}}}, Layout: Layout{Regions: []LayoutRegion{{Type: "form", ReadingOrder: 1, Text: "Save"}}}, Semantics: Semantics{Scene: "web UI", Entities: []SemanticEntity{}, Relations: []SemanticRelation{}}, Uncertainty: []string{"CSS cause not visible"}}
}

func TestToolImageProcessorUsesConfiguredVisionModelForVisionCapableMain(t *testing.T) {
	d := &fakeEvidenceDescriber{evidence: smallEvidence()}
	p := NewToolImageProcessor("p/vision", d, nil)
	out := p.ProcessToolImages(context.Background(), ToolImageInput{ToolName: "read_file", ToolText: "ok", Images: []string{"data:image/png;base64,AA=="}, ModelSupportsImages: true})
	if d.calls != 1 || len(out.Images) != 0 || !out.Success || !strings.Contains(out.Text, `schema="modlens-v2"`) {
		t.Fatalf("out=%+v calls=%d", out, d.calls)
	}
}

func TestToolImageProcessorInjectsModLensEvidenceForTextModel(t *testing.T) {
	d := &fakeEvidenceDescriber{evidence: smallEvidence()}
	p := NewToolImageProcessor("p/vision", d, nil)
	out := p.ProcessToolImages(context.Background(), ToolImageInput{ToolName: "browser", ToolText: "screenshot", Images: []string{"data:image/png;base64,AA=="}, ModelRef: "p/text"})
	if !out.Success || d.calls != 1 || len(out.Images) != 0 {
		t.Fatalf("out=%+v calls=%d", out, d.calls)
	}
	for _, want := range []string{"schema=\"modlens-v2\"", "DIRECT_EVIDENCE", "UNCERTAINTY", "Never convert uncertainty into fact"} {
		if !strings.Contains(out.Text, want) {
			t.Fatalf("missing %q in %s", want, out.Text)
		}
	}
	if len(out.VisualAnalyses) != 1 {
		t.Fatalf("visual analyses = %+v, want one tool-media record", out.VisualAnalyses)
	}
	record := out.VisualAnalyses[0]
	if record.ID == "" || record.Initiator != "tool_media_bridge" || record.Status != "ready" || record.Summary != "button clipped" || record.MediaCount != 1 {
		t.Fatalf("tool-media visual record = %+v", record)
	}
}

func TestToolImageProcessorUsesStructuredVisionProgress(t *testing.T) {
	d := &fakeEvidenceDescriber{evidence: smallEvidence()}
	events := make([]event.Event, 0, 4)
	p := NewToolImageProcessor("p/vision", d, event.FuncSink(func(e event.Event) { events = append(events, e) }))
	p.ProcessToolImages(context.Background(), ToolImageInput{ToolName: "browser", ToolText: "screenshot", Images: []string{"data:image/png;base64,AA=="}})

	hasPreparing := false
	ready := 0
	analysisID := ""
	for _, e := range events {
		if e.Kind == event.Phase || e.Kind == event.Notice {
			t.Fatalf("tool image processor emitted unstructured progress: %+v", e)
		}
		if e.Kind == event.VisionProgress && e.VisionProgress != nil && e.VisionProgress.Stage == event.VisionStagePreparing {
			hasPreparing = true
		}
		if e.Kind == event.VisionProgress && e.VisionProgress != nil {
			if analysisID == "" {
				analysisID = e.VisionProgress.AnalysisID
			}
			if e.VisionProgress.AnalysisID != analysisID || e.VisionProgress.Initiator != "tool_media_bridge" || e.VisionProgress.MediaCount != 1 {
				t.Fatalf("tool image progress identity = %+v", e.VisionProgress)
			}
		}
		if e.Kind == event.VisionProgress && e.VisionProgress != nil && e.VisionProgress.Stage == event.VisionStageReady {
			ready++
		}
	}
	if !hasPreparing {
		t.Fatal("tool image processor emitted no structured preparing progress")
	}
	if ready != 1 {
		t.Fatalf("ready events = %d, want exactly one", ready)
	}
	if analysisID == "" {
		t.Fatal("tool image progress has no analysis id")
	}
}

func TestToolImageProcessorAndProviderDescriberEmitOneFailureTerminal(t *testing.T) {
	events := make([]event.Event, 0, 8)
	sink := event.FuncSink(func(e event.Event) { events = append(events, e) })
	prov := &captureProvider{chunks: []provider.Chunk{{Type: provider.ChunkError, Err: errors.New("boom")}}}
	d := NewProviderDescriber(prov, nil, sink)
	p := NewToolImageProcessor("p/vision", d, sink)
	p.maxAttempts = 1
	p.ProcessToolImages(context.Background(), ToolImageInput{ToolName: "browser", ToolText: "shot", Images: []string{"data:image/png;base64,AA=="}})
	terminal := 0
	for _, e := range events {
		if e.Kind == event.VisionProgress && e.VisionProgress != nil && (e.VisionProgress.Stage == event.VisionStageFailed || e.VisionProgress.Stage == event.VisionStageCancelled) {
			terminal++
		}
	}
	if terminal != 1 {
		t.Fatalf("terminal events = %d, want exactly one: %+v", terminal, events)
	}
}

func TestToolImageProcessorPreservesCancellationTerminal(t *testing.T) {
	events := make([]event.Event, 0, 4)
	d := &fakeEvidenceDescriber{err: context.Canceled}
	p := NewToolImageProcessor("p/vision", d, event.FuncSink(func(e event.Event) { events = append(events, e) }))
	p.maxAttempts = 1
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.ProcessToolImages(ctx, ToolImageInput{ToolName: "browser", ToolText: "shot", Images: []string{"data:image/png;base64,AA=="}})
	var terminal []event.VisionProgressStage
	for _, e := range events {
		if e.Kind == event.VisionProgress && e.VisionProgress != nil && (e.VisionProgress.Stage == event.VisionStageFailed || e.VisionProgress.Stage == event.VisionStageCancelled) {
			terminal = append(terminal, e.VisionProgress.Stage)
		}
	}
	if len(terminal) != 1 || terminal[0] != event.VisionStageCancelled {
		t.Fatalf("terminal stages = %v, want one cancelled", terminal)
	}
}

func TestToolImageProcessorWithoutFallbackDropsImagesAndAddsHonestStatus(t *testing.T) {
	p := NewToolImageProcessor("", nil, nil)
	out := p.ProcessToolImages(context.Background(), ToolImageInput{ToolName: "browser", ToolText: "x", Images: []string{"data:image/png;base64,AA=="}})
	if len(out.Images) != 0 || !strings.Contains(out.Text, "must not claim") {
		t.Fatalf("out=%+v", out)
	}
}

func TestToolImageProcessorRetriesAndPreservesEvidenceWrapperWithinBudget(t *testing.T) {
	d := &fakeEvidenceDescriber{evidence: Evidence{
		Summary:     strings.Repeat("summary ", 1200),
		OCR:         OCR{FullText: strings.Repeat("visible text ", 1200), Lines: []OCRLine{}},
		Layout:      Layout{Regions: []LayoutRegion{}},
		Semantics:   Semantics{Scene: "dense screenshot", Entities: []SemanticEntity{}, Relations: []SemanticRelation{}},
		Uncertainty: []string{"fine details may be unreadable"},
	}}
	p := NewToolImageProcessor("p/vision", d, nil)
	out := p.ProcessToolImages(context.Background(), ToolImageInput{
		ToolName: "read_file", ToolText: strings.Repeat("T", DefaultToolResultTextBytes),
		Images: []string{"data:image/png;base64,AA=="}, ModelRef: "p/text",
	})
	if !out.Success || out.Attempts != 1 {
		t.Fatalf("out=%+v", out)
	}
	if len(out.Text) > DefaultToolResultTextBytes {
		t.Fatalf("final tool text = %d bytes, want <= %d", len(out.Text), DefaultToolResultTextBytes)
	}
	if strings.Count(out.Text, `<visual-evidence schema="modlens-v2"`) != 1 || strings.Count(out.Text, `</visual-evidence>`) != 1 {
		t.Fatalf("visual evidence wrapper was truncated or duplicated")
	}
}

func TestToolImageProcessorStopsAfterThreeFailures(t *testing.T) {
	d := &fakeEvidenceDescriber{err: context.DeadlineExceeded}
	p := NewToolImageProcessor("p/vision", d, nil)
	out := p.ProcessToolImages(context.Background(), ToolImageInput{ToolName: "read_file", ToolText: "x", Images: []string{"data:image/png;base64,AA=="}})
	if out.Success || out.Attempts != MaxToolImageAttempts || d.calls != MaxToolImageAttempts || len(out.Images) != 0 {
		t.Fatalf("out=%+v calls=%d", out, d.calls)
	}
	if !strings.Contains(out.Text, "must not claim") {
		t.Fatalf("missing honesty status: %s", out.Text)
	}
}
