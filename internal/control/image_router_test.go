package control

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/vision"
)

type routeEvidenceDescriber struct {
	calls int
	errs  []error
}

func routeTestEvidence() vision.Evidence {
	return vision.Evidence{Summary: "dialog clipped", OCR: vision.OCR{Lines: []vision.OCRLine{{Text: "Save"}}}, Layout: vision.Layout{Regions: []vision.LayoutRegion{{Type: "form", ReadingOrder: 1, Text: "Save"}}}, Semantics: vision.Semantics{Scene: "web UI", Entities: []vision.SemanticEntity{}, Relations: []vision.SemanticRelation{}}, Uncertainty: []string{"root cause not visible"}}
}

func (d *routeEvidenceDescriber) DescribeOnce(context.Context, string, []vision.Image, string) (vision.Evidence, *provider.Usage, error) {
	idx := d.calls
	d.calls++
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

func TestRouteImagesUsesModLensEvidenceForTextMainModel(t *testing.T) {
	root := t.TempDir()
	writeImageRouteConfig(t, root)
	d := &routeEvidenceDescriber{}
	c := &Controller{workspaceRoot: root, modelRef: "text/main", visionModelRef: "vision/vl", visionDescriber: d}
	res := c.routeImagesOnce(context.Background(), &ImageRouteState{}, "fix it", "why clipped?", []ResolvedImage{{Ref: "shot.png", Path: "shot.png", DataURL: "data:image/png;base64,AA=="}})
	if res.Mode != ImageRouteVisionEvidence || d.calls != 1 || len(res.Images) != 0 {
		t.Fatalf("res=%+v calls=%d", res, d.calls)
	}
	for _, want := range []string{"schema=\"modlens-v2\"", "DIRECT_EVIDENCE", "UNCERTAINTY", "Never convert uncertainty into fact"} {
		if !strings.Contains(res.Input, want) {
			t.Fatalf("missing %q: %s", want, res.Input)
		}
	}
}

func TestRouteImagesKeepsNativeImageForVisionMainModel(t *testing.T) {
	root := t.TempDir()
	writeImageRouteConfig(t, root)
	c := &Controller{workspaceRoot: root, modelRef: "vision/vl", visionModelRef: "vision/vl"}
	res := c.routeImagesOnce(context.Background(), &ImageRouteState{}, "look", "look", []ResolvedImage{{DataURL: "data:image/png;base64,AA=="}})
	if res.Mode != ImageRouteDirectMain || len(res.Images) != 1 {
		t.Fatalf("res=%+v", res)
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
