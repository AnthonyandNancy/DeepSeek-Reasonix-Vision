package boot

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

func writeBootToolImage(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create image: %v", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		t.Fatalf("encode image: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close image: %v", err)
	}
}

func toolImageWiringConfig() string {
	return `
default_model = "main"

[agent]
system_prompt = "BASE"
vision_model = "vision/vision-x"

[[providers]]
name = "main"
kind = "boot-token-profile-test"
model = "text-x"

[[providers]]
name = "vision"
kind = "boot-token-profile-test"
model = "vision-x"
vision_models = ["vision-x"]
`
}

const bootToolImageEvidence = `{"summary":"error screenshot","ocr":{"full_text":"错误码 500","lines":[{"text":"错误码 500"}]},"layout":{"regions":[{"type":"code","reading_order":1,"text":"错误码 500"}]},"semantics":{"scene":"error screenshot","entities":[],"relations":[]},"uncertainty":[]}`

func assertBootToolImageHandoff(t *testing.T, reqs []provider.Request) {
	t.Helper()
	if len(reqs) != 5 {
		t.Fatalf("provider requests = %d, want parent + child read + vision + child final + parent final", len(reqs))
	}
	visionReq := reqs[2]
	if len(visionReq.Tools) != 0 {
		t.Fatalf("vision request received %d tools, want none", len(visionReq.Tools))
	}
	hasImage := false
	for _, msg := range visionReq.Messages {
		if len(msg.Images) > 0 {
			hasImage = true
		}
	}
	if !hasImage {
		t.Fatal("third request is not the tool-image vision handoff")
	}
	foundEvidence := false
	for _, msg := range reqs[3].Messages {
		if msg.Role == provider.RoleTool && msg.Name == "read_file" &&
			strings.Contains(msg.Content, `schema="modlens-v2"`) &&
			strings.Contains(msg.Content, "错误码 500") {
			foundEvidence = true
		}
	}
	if !foundEvidence {
		t.Fatal("child follow-up did not receive ModLens v2 visual evidence")
	}
}

// TestBuildTaskChildReceivesToolImageProcessor reproduces the old fork's
// Balanced/Full boot-order bug: task tools constructed before the processor
// existed could permanently copy nil into their children.
func TestBuildTaskChildReceivesToolImageProcessor(t *testing.T) {
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	writeBootToolImage(t, filepath.Join(dir, "shot.png"))

	registerBootTokenProfileTestProvider()
	prov := testutil.NewMock("tool-image-task-wiring",
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "task-1", Name: "task", Arguments: `{"prompt":"检查 shot.png"}`}}},
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "read-1", Name: "read_file", Arguments: `{"path":"shot.png"}`}}},
		testutil.Turn{Text: bootToolImageEvidence},
		testutil.Turn{Text: "child complete"},
		testutil.Turn{Text: "parent complete"},
	)
	setBootTokenProfileTestProvider(t, prov)
	writeFile(t, dir, "reasonix.toml", toolImageWiringConfig())

	ctrl, err := Build(context.Background(), Options{Sink: event.Discard})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer ctrl.Close()
	if err := ctrl.Run(context.Background(), "delegate image inspection"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertBootToolImageHandoff(t, prov.Requests())
}

// TestBuildSkillChildReceivesToolImageProcessor pins the separate skill
// sub-agent construction path (review/research/explore/custom skills).
func TestBuildSkillChildReceivesToolImageProcessor(t *testing.T) {
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	writeBootToolImage(t, filepath.Join(dir, "shot.png"))

	registerBootTokenProfileTestProvider()
	prov := testutil.NewMock("tool-image-skill-wiring",
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "review-1", Name: "review", Arguments: `{"task":"检查 shot.png"}`}}},
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "read-1", Name: "read_file", Arguments: `{"path":"shot.png"}`}}},
		testutil.Turn{Text: bootToolImageEvidence},
		testutil.Turn{Text: "skill complete"},
		testutil.Turn{Text: "parent complete"},
	)
	setBootTokenProfileTestProvider(t, prov)
	writeFile(t, dir, "reasonix.toml", toolImageWiringConfig())

	ctrl, err := Build(context.Background(), Options{Sink: event.Discard})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer ctrl.Close()
	if err := ctrl.Run(context.Background(), "review image inspection"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertBootToolImageHandoff(t, prov.Requests())
}

func TestBuildRegistersRootAnalyzeMediaVisionTool(t *testing.T) {
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)

	registerBootTokenProfileTestProvider()
	prov := testutil.NewMock("root-analyze-media-wiring",
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "vision-1", Name: "analyze_media_with_vision", Arguments: `{}`}}},
		testutil.Turn{Text: bootToolImageEvidence},
		testutil.Turn{Text: "analysis complete"},
	)
	setBootTokenProfileTestProvider(t, prov)
	writeFile(t, dir, "reasonix.toml", toolImageWiringConfig())

	ctrl, err := Build(context.Background(), Options{Sink: event.Discard})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer ctrl.Close()
	found := false
	for _, entry := range ctrl.ToolContractEntries() {
		if entry.Name == "analyze_media_with_vision" {
			found = true
		}
	}
	if !found {
		t.Fatalf("root registry missing analyze_media_with_vision: %+v", ctrl.ToolContractEntries())
	}
	ctrl.Executor().Session().Add(provider.Message{
		Role: provider.RoleTool, Name: provider.LocalOnlyToolName, ToolCallID: provider.LocalOnlyToolID,
		LocalOnly: true, Images: []string{"data:image/png;base64,AA=="},
	})
	if err := ctrl.Run(context.Background(), "重新分析上一张图片"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	reqs := prov.Requests()
	if len(reqs) != 3 {
		t.Fatalf("provider requests = %d, want main tool call + vision + main continuation", len(reqs))
	}
	if len(reqs[1].Messages) != 2 || len(reqs[1].Messages[1].Images) != 1 {
		t.Fatalf("second request is not independent vision analysis: %+v", reqs[1].Messages)
	}
	foundResult := false
	for _, message := range reqs[2].Messages {
		if message.Role == provider.RoleTool && message.Name == "analyze_media_with_vision" && strings.Contains(message.Content, `schema="modlens-v2"`) {
			foundResult = true
		}
	}
	if !foundResult {
		t.Fatalf("main continuation missing ModLens tool result: %+v", reqs[2].Messages)
	}
	foundRecord := false
	for _, message := range ctrl.History() {
		if message.Role == provider.RoleTool && message.Name == "analyze_media_with_vision" && len(message.VisualAnalyses) == 1 && message.VisualAnalyses[0].Initiator == "main_model_tool" {
			foundRecord = true
		}
	}
	if !foundRecord {
		t.Fatalf("history missing main-model visual analysis record: %+v", ctrl.History())
	}
}

func TestBuildOmitsAnalyzeMediaVisionToolWithoutUsableVisionModel(t *testing.T) {
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	registerBootTokenProfileTestProvider()
	setBootTokenProfileTestProvider(t, testutil.NewMock("no-root-analyze-media"))
	writeFile(t, dir, "reasonix.toml", `
default_model = "main"

[agent]
system_prompt = "BASE"

[[providers]]
name = "main"
kind = "boot-token-profile-test"
model = "text-x"
`)
	ctrl, err := Build(context.Background(), Options{Sink: event.Discard})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer ctrl.Close()
	for _, entry := range ctrl.ToolContractEntries() {
		if entry.Name == "analyze_media_with_vision" {
			t.Fatalf("unusable vision configuration registered root analysis tool: %+v", entry)
		}
	}
}
