package vision

import (
	"context"
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

func TestToolImageProcessorKeepsImagesForVisionCapableModel(t *testing.T) {
	d := &fakeEvidenceDescriber{evidence: smallEvidence()}
	p := NewToolImageProcessor("p/vision", d, nil)
	out := p.ProcessToolImages(context.Background(), ToolImageInput{ToolName: "read_file", ToolText: "ok", Images: []string{"data:image/png;base64,AA=="}, ModelSupportsImages: true})
	if d.calls != 0 || len(out.Images) != 1 || out.Text != "ok" {
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
}

func TestToolImageProcessorUsesStructuredVisionProgress(t *testing.T) {
	d := &fakeEvidenceDescriber{evidence: smallEvidence()}
	events := make([]event.Event, 0, 4)
	p := NewToolImageProcessor("p/vision", d, event.FuncSink(func(e event.Event) { events = append(events, e) }))
	p.ProcessToolImages(context.Background(), ToolImageInput{ToolName: "browser", ToolText: "screenshot", Images: []string{"data:image/png;base64,AA=="}})

	hasPreparing := false
	for _, e := range events {
		if e.Kind == event.Phase || e.Kind == event.Notice {
			t.Fatalf("tool image processor emitted unstructured progress: %+v", e)
		}
		if e.Kind == event.VisionProgress && e.VisionProgress != nil && e.VisionProgress.Stage == event.VisionStagePreparing {
			hasPreparing = true
		}
	}
	if !hasPreparing {
		t.Fatal("tool image processor emitted no structured preparing progress")
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
