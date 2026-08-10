package control

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/vision"
)

type routeEvidenceDescriber struct {
	calls        int
	errs         []error
	imageBatches [][]vision.Image
}

type routeLifecycleProvider struct {
	streamErr error
}

func (p *routeLifecycleProvider) Name() string { return "route-lifecycle" }
func (p *routeLifecycleProvider) Stream(_ context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	if p.streamErr != nil {
		return nil, p.streamErr
	}
	ch := make(chan provider.Chunk, 1)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: `{"summary":"dialog clipped","ocr":{"full_text":"Save","lines":[{"text":"Save"}]},"layout":{"regions":[{"type":"form","reading_order":1,"text":"Save"}]},"semantics":{"scene":"web UI","entities":[],"relations":[]},"uncertainty":["root cause not visible"]}`}
	close(ch)
	return ch, nil
}

func routeTestEvidence() vision.Evidence {
	return vision.Evidence{Summary: "dialog clipped", OCR: vision.OCR{Lines: []vision.OCRLine{{Text: "Save"}}}, Layout: vision.Layout{Regions: []vision.LayoutRegion{{Type: "form", ReadingOrder: 1, Text: "Save"}}}, Semantics: vision.Semantics{Scene: "web UI", Entities: []vision.SemanticEntity{}, Relations: []vision.SemanticRelation{}}, Uncertainty: []string{"root cause not visible"}}
}

func (d *routeEvidenceDescriber) DescribeOnce(_ context.Context, _ string, images []vision.Image, _ string) (vision.Evidence, *provider.Usage, error) {
	idx := d.calls
	d.calls++
	d.imageBatches = append(d.imageBatches, append([]vision.Image(nil), images...))
	if idx < len(d.errs) && d.errs[idx] != nil {
		return vision.Evidence{}, nil, d.errs[idx]
	}
	return routeTestEvidence(), nil, nil
}
func (d *routeEvidenceDescriber) DescribeToolImagesOnce(context.Context, string, vision.ToolImageDescribeInput) (vision.Evidence, *provider.Usage, error) {
	panic("unexpected")
}

func writeImageRouteConfig(t *testing.T, root string) {
	t.Helper()
	cfg := `default_model = "text/main"

[[providers]]
name = "text"
kind = "openai"
base_url = "https://example.invalid"
model = "main"

[[providers]]
name = "vision"
kind = "openai"
base_url = "https://example.invalid"
model = "vl"
vision_models = ["vl"]
`
	if err := os.WriteFile(filepath.Join(root, "reasonix.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeVisionCapableImageRouteConfig(t *testing.T, root string) {
	t.Helper()
	cfg := `default_model = "main/vl"

[[providers]]
name = "main"
kind = "openai"
base_url = "https://example.invalid"
model = "vl"
vision_models = ["vl"]

[[providers]]
name = "vision"
kind = "openai"
base_url = "https://example.invalid"
model = "evidence"
vision_models = ["evidence"]
`
	if err := os.WriteFile(filepath.Join(root, "reasonix.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRouteImagesUsesModLensEvidenceForTextMainModel(t *testing.T) {
	root := t.TempDir()
	writeImageRouteConfig(t, root)
	d := &routeEvidenceDescriber{}
	c := &Controller{workspaceRoot: root, modelRef: "text/main", visionModelRef: "vision/vl", visionDescriber: d}
	res := c.routeImagesOnce(context.Background(), &ImageRouteState{}, "fix it", "why clipped?", []ResolvedImage{{Ref: "shot.png", Path: "shot.png", DataURL: "data:image/png;base64,AA=="}})
	if res.Mode != ImageRouteVisionEvidence || d.calls != 1 || len(res.Images) != 0 {
		t.Fatalf("res=%+v calls=%d", res, d.calls)
	}
	// The turn receives the gist under the same trusted wrapper; DIRECT_EVIDENCE
	// stays in the parked record rather than the model's context.
	for _, want := range []string{"schema=\"modlens-v2-digest\"", "SUMMARY", "UNCERTAINTY", "never convert uncertainty into fact"} {
		if !strings.Contains(res.Input, want) {
			t.Fatalf("missing %q: %s", want, res.Input)
		}
	}
	if strings.Contains(res.Input, "DIRECT_EVIDENCE") {
		t.Fatalf("full ModLens record leaked into the turn: %s", res.Input)
	}
	if len(res.VisualAnalyses) != 1 {
		t.Fatalf("visual analyses = %+v, want one durable record", res.VisualAnalyses)
	}
	record := res.VisualAnalyses[0]
	if record.ID == "" || record.Initiator != "host_auto" || record.ModelRef != "vision/vl" || record.Status != "ready" {
		t.Fatalf("visual analysis identity/status = %+v", record)
	}
	if record.Summary != "dialog clipped" || record.OCRText != "Save" || !strings.Contains(record.Evidence, "modlens-v2") {
		t.Fatalf("visual analysis evidence = %+v", record)
	}
	if record.MediaCount != 1 || len(record.MediaRefs) != 1 || record.MediaRefs[0] != "shot.png" {
		t.Fatalf("visual analysis media = %+v", record)
	}
}

func TestRouteImagesPassesAllUserImagesToVisionAndRemovesRawRefsFromMainInput(t *testing.T) {
	root := t.TempDir()
	writeImageRouteConfig(t, root)
	d := &routeEvidenceDescriber{}
	c := &Controller{workspaceRoot: root, modelRef: "text/main", visionModelRef: "vision/vl", visionDescriber: d}
	input := "Referenced context:\n\n<image path=\"a.png\">\n[image note]\n</image>\n\ncompare @a.png and @b.png"
	images := []ResolvedImage{
		{Ref: "@a.png", Path: "a.png", DataURL: "data:image/png;base64,AA=="},
		{Ref: "@b.png", Path: "b.png", DataURL: "data:image/png;base64,BB=="},
	}

	res := c.routeImagesOnce(context.Background(), &ImageRouteState{}, input, "compare the images", images)
	if res.Mode != ImageRouteVisionEvidence {
		t.Fatalf("route mode = %v, want visual evidence", res.Mode)
	}
	if len(d.imageBatches) != 1 || len(d.imageBatches[0]) != 2 {
		t.Fatalf("vision image batches = %+v, want one batch containing both images", d.imageBatches)
	}
	if strings.Contains(res.Input, "@a.png") || strings.Contains(res.Input, "@b.png") || strings.Contains(res.Input, "<image path=") {
		t.Fatalf("main model input still exposes raw user image refs: %q", res.Input)
	}
}

func TestRouteImagesUsesStructuredVisionProgressInsteadOfHardcodedPhase(t *testing.T) {
	root := t.TempDir()
	writeImageRouteConfig(t, root)
	var events []event.Event
	sink := event.FuncSink(func(e event.Event) { events = append(events, e) })
	d := vision.NewProviderDescriber(&routeLifecycleProvider{}, nil, sink)
	c := &Controller{
		workspaceRoot:   root,
		modelRef:        "text/main",
		visionModelRef:  "vision/vl",
		visionDescriber: d,
		sink:            sink,
	}
	c.routeImagesOnce(context.Background(), &ImageRouteState{}, "fix it", "why clipped?", []ResolvedImage{{DataURL: "data:image/png;base64,AA=="}})

	preparing := 0
	ready := 0
	for _, e := range events {
		if e.Kind == event.Phase || e.Kind == event.Notice {
			if strings.Contains(e.Text, "Extracting ModLens") || strings.Contains(e.Text, "Visual evidence ready") {
				t.Fatalf("vision route emitted hardcoded user text: %+v", e)
			}
		}
		if e.Kind == event.VisionProgress && e.VisionProgress != nil && e.VisionProgress.Stage == event.VisionStagePreparing {
			preparing++
		}
		if e.Kind == event.VisionProgress && e.VisionProgress != nil && e.VisionProgress.Stage == event.VisionStageReady {
			ready++
		}
	}
	if preparing != 1 || ready != 1 {
		t.Fatalf("preparing=%d ready=%d events=%+v, want one describer lifecycle", preparing, ready, events)
	}
}

func TestRouteImagesPreservesDescriberFailureDetailWithoutDuplicateTerminal(t *testing.T) {
	root := t.TempDir()
	writeImageRouteConfig(t, root)
	var events []event.Event
	sink := event.FuncSink(func(e event.Event) { events = append(events, e) })
	d := vision.NewProviderDescriber(&routeLifecycleProvider{streamErr: context.DeadlineExceeded}, nil, sink)
	c := &Controller{
		workspaceRoot: root, modelRef: "text/main", visionModelRef: "vision/vl",
		visionDescriber: d, sink: sink,
	}
	res := c.routeImagesOnce(context.Background(), &ImageRouteState{}, "look", "look", []ResolvedImage{{DataURL: "data:image/png;base64,AA=="}})
	failed := 0
	for _, e := range events {
		if e.Kind == event.VisionProgress && e.VisionProgress != nil && e.VisionProgress.Stage == event.VisionStageFailed {
			failed++
			if e.VisionProgress.Detail != "timeout" {
				t.Fatalf("failure detail = %q, want timeout", e.VisionProgress.Detail)
			}
		}
	}
	if failed != maxVisionAttemptsPerTurn {
		t.Fatalf("failed events=%d, want one per attempt (%d)", failed, maxVisionAttemptsPerTurn)
	}
	if len(res.VisualAnalyses) != 1 {
		t.Fatalf("visual analyses = %+v", res.VisualAnalyses)
	}
	for _, stage := range res.VisualAnalyses[0].Stages {
		if stage.Stage == string(event.VisionStageFailed) && stage.Detail != "timeout" {
			t.Fatalf("recorded failure detail overwritten: %+v", stage)
		}
	}
}

func TestRouteImagesDirectsToVisionCapableMainModel(t *testing.T) {
	root := t.TempDir()
	writeVisionCapableImageRouteConfig(t, root)
	d := &routeEvidenceDescriber{}
	c := &Controller{workspaceRoot: root, modelRef: "main/vl", visionModelRef: "vision/evidence", visionDescriber: d}
	res := c.routeImagesOnce(context.Background(), &ImageRouteState{}, "look", "look", []ResolvedImage{{DataURL: "data:image/png;base64,AA=="}})
	if res.Mode != ImageRouteDirectMain || len(res.Images) != 1 || d.calls != 0 {
		t.Fatalf("res=%+v calls=%d", res, d.calls)
	}
}

func TestRouteImagesUsesIndependentVisionWhenRequired(t *testing.T) {
	root := t.TempDir()
	writeVisionCapableImageRouteConfig(t, root)
	d := &routeEvidenceDescriber{}
	c := &Controller{workspaceRoot: root, modelRef: "main/vl", visionModelRef: "vision/evidence", visionDescriber: d}
	res := c.routeImagesOnce(context.Background(), &ImageRouteState{RequireIndependentVision: true}, "look", "look", []ResolvedImage{{DataURL: "data:image/png;base64,AA=="}})
	if res.Mode != ImageRouteVisionEvidence || d.calls != 1 {
		t.Fatalf("res=%+v calls=%d", res, d.calls)
	}
}

func TestRouteImagesRetriesAtMostThreeAndSucceedsOnThird(t *testing.T) {
	root := t.TempDir()
	writeImageRouteConfig(t, root)
	d := &routeEvidenceDescriber{errs: []error{errors.New("first"), errors.New("second"), nil}}
	state := &ImageRouteState{}
	c := &Controller{workspaceRoot: root, modelRef: "text/main", visionModelRef: "vision/vl", visionDescriber: d}
	res := c.routeImagesOnce(context.Background(), state, "fix it", "why?", []ResolvedImage{{Ref: "shot.png", Path: "shot.png", DataURL: "data:image/png;base64,AA=="}})
	if res.Mode != ImageRouteVisionEvidence || d.calls != maxVisionAttemptsPerTurn || state.VisionAttempts != maxVisionAttemptsPerTurn {
		t.Fatalf("res=%+v calls=%d state=%+v", res, d.calls, state)
	}
}

func TestRouteImagesFailureDegradesWithoutClaimingPixels(t *testing.T) {
	root := t.TempDir()
	writeImageRouteConfig(t, root)
	d := &routeEvidenceDescriber{errs: []error{errors.New("fail"), errors.New("fail"), errors.New("fail")}}
	c := &Controller{workspaceRoot: root, modelRef: "text/main", visionModelRef: "vision/vl", visionDescriber: d}
	res := c.routeImagesOnce(context.Background(), &ImageRouteState{}, "look", "look", []ResolvedImage{{Ref: "shot.png", Path: "shot.png", DataURL: "data:image/png;base64,AA=="}})
	if res.Mode != ImageRoutePathOnly || d.calls != maxVisionAttemptsPerTurn || !strings.Contains(res.Input, "Do not claim to have seen") {
		t.Fatalf("res=%+v calls=%d", res, d.calls)
	}
}

func TestReanalysisUsesVisionCapableMainModelWithoutIndependentGuidance(t *testing.T) {
	root := t.TempDir()
	writeVisionCapableImageRouteConfig(t, root)
	d := &routeEvidenceDescriber{errs: []error{errors.New("vision unavailable"), errors.New("vision unavailable"), errors.New("vision unavailable")}}
	c := &Controller{workspaceRoot: root, modelRef: "main/vl", visionModelRef: "vision/evidence", visionDescriber: d}
	res := c.routeResolvedMediaOnce(context.Background(), &ImageRouteState{}, "reanalyze", "reanalyze", MediaTurnResolution{
		Images:              []ResolvedImage{{DataURL: "data:image/png;base64,AA=="}},
		ReanalysisRequested: true,
	})
	if res.Mode != ImageRouteDirectMain || len(res.Images) != 1 || d.calls != 0 {
		t.Fatalf("reanalysis route = %+v calls=%d", res, d.calls)
	}
	if strings.Contains(res.Input, "<visual-model-assistance>") || strings.Contains(res.Input, "<visual-reanalysis-request>") {
		t.Fatalf("reanalysis route injected independent-vision guidance: %q", res.Input)
	}
}

func TestRouteImagesResolutionFailureSkipsVisionModel(t *testing.T) {
	root := t.TempDir()
	writeImageRouteConfig(t, root)
	d := &routeEvidenceDescriber{}
	c := &Controller{workspaceRoot: root, modelRef: "text/main", visionModelRef: "vision/vl", visionDescriber: d}
	res := c.routeImagesOnce(context.Background(), &ImageRouteState{}, "look", "look", []ResolvedImage{{Ref: "bad.png", Path: "bad.png", Error: "unreadable"}})
	if res.Mode != ImageRoutePathOnly || d.calls != 0 {
		t.Fatalf("res=%+v calls=%d", res, d.calls)
	}
}

func TestRouteImagesStatePreventsDuplicateVisionCall(t *testing.T) {
	root := t.TempDir()
	writeImageRouteConfig(t, root)
	d := &routeEvidenceDescriber{}
	c := &Controller{workspaceRoot: root, modelRef: "text/main", visionModelRef: "vision/vl", visionDescriber: d}
	state := &ImageRouteState{}
	images := []ResolvedImage{{Ref: "shot.png", Path: "shot.png", DataURL: "data:image/png;base64,AA=="}}
	first := c.routeImagesOnce(context.Background(), state, "look", "look", images)
	second := c.routeImagesOnce(context.Background(), state, "look", "look", images)
	if first.Mode != ImageRouteVisionEvidence || second.Mode != ImageRoutePathOnly || d.calls != 1 {
		t.Fatalf("first=%+v second=%+v calls=%d", first, second, d.calls)
	}
}
