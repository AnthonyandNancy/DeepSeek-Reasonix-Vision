package control

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
	"reasonix/internal/vision"
)

// Several images produce several evidence blocks. Without a label they are
// indistinguishable in history, and "the second image" becomes unaddressable.
func TestEvidenceBlockCarriesAddressableMediaLabel(t *testing.T) {
	workspace := t.TempDir()
	writeImageRouteConfig(t, workspace)
	sess := agent.NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleUser, Images: []string{"data:image/png;base64,first"}})
	exec := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	c := New(Options{
		Executor: exec, WorkspaceRoot: workspace, ModelRef: "text/main", VisionModelRef: "vision/vl",
		VisionDescriber: &routeEvidenceDescriber{},
	})

	if base := c.conversationMediaIndexBase(); base != 1 {
		t.Fatalf("conversation media base = %d, want 1 stored image", base)
	}
	route := c.routeResolvedMediaOnce(context.Background(), &ImageRouteState{}, "看这张 @shot.png", "看这张", MediaTurnResolution{
		Images: []ResolvedImage{{Ref: ".reasonix/attachments/shot.png", DataURL: "data:image/png;base64," + tinyPNG}},
	})
	if !strings.Contains(route.Input, `index="2"`) {
		t.Fatalf("evidence block lost its conversation index:\n%s", route.Input)
	}
	if !strings.Contains(route.Input, `ref="shot.png"`) {
		t.Fatalf("evidence block lost its media ref:\n%s", route.Input)
	}
}

// The label is only useful if it addresses the image analyze_media_with_vision
// would return for the same number. Both must flatten-then-dedupe identically.
func TestMediaIndexBaseMatchesToolImageIndex(t *testing.T) {
	sess := agent.NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleUser, Images: []string{"data:image/png;base64,first"}})
	sess.Add(provider.Message{Role: provider.RoleUser, Images: []string{"data:image/png;base64,second"}})
	exec := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	c := New(Options{Executor: exec, ModelRef: "text/main", VisionModelRef: "vision/vl"})

	if base := c.conversationMediaIndexBase(); base != 2 {
		t.Fatalf("media base = %d, want 2", base)
	}
	// A block labelled index="2" must resolve through image_index=2 (zero-based 1).
	images, _, err := c.ResolveHistoricalVisionMedia(context.Background(), vision.MediaSelection{Index: 1})
	if err != nil || len(images) != 1 || images[0].DataURL != "data:image/png;base64,second" {
		t.Fatalf("image_index=2 resolved %+v (err=%v), want the second image", images, err)
	}
}

// A batch analysed as one block cannot claim a single position.
func TestMultiImageEvidenceOmitsIndexButKeepsRefs(t *testing.T) {
	id := evidenceMediaID(0, []ResolvedImage{
		{Ref: ".reasonix/attachments/a.png"},
		{Ref: ".reasonix/attachments/b.png"},
	})
	if id.Index != 0 {
		t.Fatalf("multi-image batch claimed index %d", id.Index)
	}
	if id.Ref != "a.png, b.png" {
		t.Fatalf("multi-image refs = %q, want both names", id.Ref)
	}
}

// The turn carries the gist, not the whole ModLens record. A screenshot's OCR
// and layout run to kilobytes that mostly go unread, and a few images would
// crowd out the rest of the conversation.
func TestUserImageTurnCarriesDigestNotFullEvidence(t *testing.T) {
	workspace := t.TempDir()
	writeImageRouteConfig(t, workspace)
	c := New(Options{
		WorkspaceRoot: workspace, ModelRef: "text/main", VisionModelRef: "vision/vl",
		VisionDescriber: &routeEvidenceDescriber{},
	})
	route := c.routeResolvedMediaOnce(context.Background(), &ImageRouteState{}, "看这张 @shot.png", "看这张", MediaTurnResolution{
		Images: []ResolvedImage{{Ref: ".reasonix/attachments/shot.png", Path: ".reasonix/attachments/shot.png", DataURL: "data:image/png;base64," + tinyPNG}},
	})
	if !strings.Contains(route.Input, "modlens-v2-digest") {
		t.Fatalf("turn did not carry a digest:\n%s", route.Input)
	}
	if strings.Contains(route.Input, "OCR full text:") || strings.Contains(route.Input, "LAYOUT[") {
		t.Fatalf("full ModLens record leaked into the turn:\n%s", route.Input)
	}
	if !strings.Contains(route.Input, "analyze_media_with_vision returns them") {
		t.Fatalf("digest did not tell the model how to get detail:\n%s", route.Input)
	}
	// The UI card must still show everything the model's eyes saw.
	if len(route.VisualAnalyses) != 1 || !strings.Contains(route.VisualAnalyses[0].Evidence, "OCR[1]") {
		t.Fatalf("transcript record lost the full evidence: %+v", route.VisualAnalyses)
	}
}

// Detail the digest omitted must be retrievable without paying for the visual
// model twice. The full rendering rides the transcript record, so it also
// survives a session reload.
func TestHostAnalysisIsRetrievableFromEvidenceStore(t *testing.T) {
	workspace := t.TempDir()
	writeImageRouteConfig(t, workspace)
	sess := agent.NewSession("system")
	exec := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	c := New(Options{
		Executor: exec, WorkspaceRoot: workspace, ModelRef: "text/main", VisionModelRef: "vision/vl",
		VisionDescriber: &routeEvidenceDescriber{},
	})
	route := c.routeResolvedMediaOnce(context.Background(), &ImageRouteState{}, "看这张", "看这张", MediaTurnResolution{
		Images: []ResolvedImage{{Ref: ".reasonix/attachments/shot.png", Path: ".reasonix/attachments/shot.png", DataURL: "data:image/png;base64," + tinyPNG}},
	})
	sess.Add(provider.Message{Role: provider.RoleUser, Content: route.Input, VisualAnalyses: route.VisualAnalyses})

	evidence, ok := c.StoredVisualEvidence(".reasonix/attachments/shot.png")
	if !ok || !strings.Contains(evidence, "OCR[1]") {
		t.Fatalf("full evidence was not retrievable: ok=%v evidence=%q", ok, evidence)
	}
	// The "@" form and separator drift must reach the same record.
	if _, ok := c.StoredVisualEvidence("@.reasonix\\attachments\\shot.png"); !ok {
		t.Fatalf("evidence key did not normalize prefix/separators")
	}
	if _, ok := c.StoredVisualEvidence(".reasonix/attachments/other.png"); ok {
		t.Fatalf("lookup matched an unrelated media ref")
	}
}

// With preview off the host reads nothing, but the model must still learn the
// media exists and is readable — otherwise it silently answers about an image
// nobody looked at.
func TestVisionPreviewOffDefersToTheModel(t *testing.T) {
	workspace := t.TempDir()
	cfg := `default_model = "text/main"

[agent]
vision_preview = false

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
	if err := os.WriteFile(filepath.Join(workspace, "reasonix.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &routeEvidenceDescriber{}
	c := New(Options{
		WorkspaceRoot: workspace, ModelRef: "text/main", VisionModelRef: "vision/vl",
		VisionDescriber: d,
	})
	route := c.routeResolvedMediaOnce(context.Background(), &ImageRouteState{}, "看这张 @shot.png", "看这张", MediaTurnResolution{
		Images: []ResolvedImage{{Ref: ".reasonix/attachments/shot.png", Path: ".reasonix/attachments/shot.png", DataURL: "data:image/png;base64," + tinyPNG}},
	})
	if d.calls != 0 {
		t.Fatalf("preview off still called the visual model %d times", d.calls)
	}
	if route.Mode != ImageRouteDeferred {
		t.Fatalf("route mode = %v, want deferred", route.Mode)
	}
	if !strings.Contains(route.Input, "analyze_media_with_vision reads conversation media") {
		t.Fatalf("deferred turn did not tell the model the media is readable:\n%s", route.Input)
	}
	if strings.Contains(route.Input, "could not read their pixels") {
		t.Fatalf("deferred turn claimed the image was unreadable:\n%s", route.Input)
	}
	if !strings.Contains(route.Input, "visual-model-assistance") {
		t.Fatalf("deferred turn lost the visual bridge advertisement:\n%s", route.Input)
	}
}

// Preview defaults on: absent config must not silently disable the gist.
func TestVisionPreviewDefaultsOn(t *testing.T) {
	workspace := t.TempDir()
	writeImageRouteConfig(t, workspace)
	c := New(Options{WorkspaceRoot: workspace, ModelRef: "text/main", VisionModelRef: "vision/vl"})
	if !c.visionPreviewEnabled() {
		t.Fatalf("vision preview defaulted off")
	}
}
