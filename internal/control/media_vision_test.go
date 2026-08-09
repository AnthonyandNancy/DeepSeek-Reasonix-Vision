package control

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestHistoricalVisionMediaResolvesLatestStoredConversationImage(t *testing.T) {
	sess := agent.NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleUser, Images: []string{"data:image/png;base64,first"}})
	sess.Add(provider.Message{Role: provider.RoleTool, LocalOnly: true, Images: []string{"data:image/png;base64,latest"}})
	exec := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	c := New(Options{Executor: exec, ModelRef: "text/main", VisionModelRef: "vision/vl"})

	images, refs, err := c.ResolveHistoricalVisionMedia(context.Background(), vision.MediaSelection{Index: -1})
	if err != nil || len(images) != 1 || images[0].DataURL != "data:image/png;base64,latest" || len(refs) != 1 {
		t.Fatalf("images=%+v refs=%v err=%v, want latest stored image", images, refs, err)
	}
}

func TestHistoricalVisionMediaLatestSelectsLastImageFromMultiImageTurn(t *testing.T) {
	sess := agent.NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleUser, Images: []string{
		"data:image/png;base64,older",
		"data:image/png;base64,newest",
	}})
	exec := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	c := New(Options{Executor: exec, ModelRef: "text/main", VisionModelRef: "vision/vl"})

	images, refs, err := c.ResolveHistoricalVisionMedia(context.Background(), vision.MediaSelection{Index: -1})
	if err != nil || len(images) != 1 || images[0].DataURL != "data:image/png;base64,newest" || len(refs) != 1 {
		t.Fatalf("images=%+v refs=%v err=%v, want final image from latest turn", images, refs, err)
	}
}

func TestReanalysisResolvesNamedHistoricalAttachmentPath(t *testing.T) {
	workspace := t.TempDir()
	writeImageRouteConfig(t, workspace)
	rel := filepath.ToSlash(filepath.Join(".reasonix", "attachments", "clipboard-20260808-000001.png"))
	path := filepath.Join(workspace, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, mustBase64(t, tinyPNG), 0o644); err != nil {
		t.Fatal(err)
	}
	sess := agent.NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "请查看 @[clipboard-20260808-000001.png](" + rel + ")"})
	exec := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	c := New(Options{Executor: exec, WorkspaceRoot: workspace, ModelRef: "text/main", VisionModelRef: "vision/vl"})

	images, refs, err := c.ResolveHistoricalVisionMedia(context.Background(), vision.MediaSelection{Index: -1})
	if err != nil || len(images) != 1 || images[0].Path != rel || images[0].DataURL == "" || len(refs) != 1 || refs[0] != rel {
		t.Fatalf("named historical images=%+v refs=%v err=%v", images, refs, err)
	}
}

func TestReanalysisUsesLegacyDisplayResolverWhenProviderTurnHasNoMediaRef(t *testing.T) {
	workspace := t.TempDir()
	writeImageRouteConfig(t, workspace)
	rel := filepath.ToSlash(filepath.Join(".reasonix", "attachments", "legacy-display.png"))
	path := filepath.Join(workspace, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, mustBase64(t, tinyPNG), 0o644); err != nil {
		t.Fatal(err)
	}

	// Older Desktop sessions may have lost the attachment reference from the
	// provider transcript while retaining it in .display.json. The controller
	// must consult that legacy display source during explicit re-analysis.
	sess := agent.NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "请重新分析刚才的图片"})
	exec := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	c := New(Options{
		Executor:       exec,
		WorkspaceRoot:  workspace,
		ModelRef:       "text/main",
		VisionModelRef: "vision/vl",
		HistoricalUserContentResolver: func(string) string {
			return fmt.Sprintf("请查看 @[legacy-display.png](%s)", rel)
		},
	})

	images, refs, err := c.ResolveHistoricalVisionMedia(context.Background(), vision.MediaSelection{Index: -1})
	if err != nil || len(images) != 1 || images[0].Path != rel || images[0].DataURL == "" || len(refs) != 1 {
		t.Fatalf("legacy display images=%+v refs=%v err=%v", images, refs, err)
	}
}

func TestHistoricalVisionMediaRejectsAbsolutePathsFromToolProse(t *testing.T) {
	workspace := t.TempDir()
	writeImageRouteConfig(t, workspace)
	path := filepath.Join(workspace, ".reasonix", "attachments", "tool-absolute.png")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, mustBase64(t, tinyPNG), 0o644); err != nil {
		t.Fatal(err)
	}

	sess := agent.NewSession("system")
	sess.Add(provider.Message{
		Role:    provider.RoleTool,
		Name:    "glob",
		Content: "image found at " + path,
	})
	exec := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	c := New(Options{Executor: exec, WorkspaceRoot: workspace, ModelRef: "text/main", VisionModelRef: "vision/vl"})

	images, refs, err := c.ResolveHistoricalVisionMedia(context.Background(), vision.MediaSelection{Index: -1})
	if err != nil || len(images) != 0 || len(refs) != 0 {
		t.Fatalf("absolute tool prose became media: images=%+v refs=%v err=%v", images, refs, err)
	}
}

func TestReanalysisUsesStructuredHistoricalMediaRefs(t *testing.T) {
	workspace := t.TempDir()
	writeImageRouteConfig(t, workspace)
	rel := filepath.ToSlash(filepath.Join(".reasonix", "attachments", "structured-ref.png"))
	path := filepath.Join(workspace, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, mustBase64(t, tinyPNG), 0o644); err != nil {
		t.Fatal(err)
	}
	sess := agent.NewSession("system")
	sess.Add(provider.Message{
		Role:      provider.RoleUser,
		Content:   "之前查看过一张图片",
		MediaRefs: []string{"@" + rel},
	})
	exec := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	c := New(Options{Executor: exec, WorkspaceRoot: workspace, ModelRef: "text/main", VisionModelRef: "vision/vl"})

	images, refs, err := c.ResolveHistoricalVisionMedia(context.Background(), vision.MediaSelection{Index: -1})
	if err != nil || len(images) != 1 || images[0].Path != rel || images[0].DataURL == "" || len(refs) != 1 || refs[0] != rel {
		t.Fatalf("structured historical media=%+v refs=%v err=%v", images, refs, err)
	}
}

func TestReanalysisReachesMainModelBeforeHistoricalVisionTool(t *testing.T) {
	workspace := t.TempDir()
	writeImageRouteConfig(t, workspace)
	rel := filepath.ToSlash(filepath.Join(".reasonix", "attachments", "legacy-run.png"))
	path := filepath.Join(workspace, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, mustBase64(t, tinyPNG), 0o644); err != nil {
		t.Fatal(err)
	}

	describer := &routeEvidenceDescriber{}
	runner := &capabilityRecordingRunner{}
	sess := agent.NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "之前查看过一张图片"})
	exec := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	c := New(Options{
		Runner:          runner,
		Executor:        exec,
		WorkspaceRoot:   workspace,
		ModelRef:        "text/main",
		VisionModelRef:  "vision/vl",
		VisionDescriber: describer,
		HistoricalUserContentResolver: func(string) string {
			return "请查看 @[legacy-run.png](" + rel + ")"
		},
	})

	if err := c.Run(context.Background(), "重新分析之前的图片"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if describer.calls != 0 {
		t.Fatalf("independent vision calls before main model = %d, want 0", describer.calls)
	}
	if !strings.Contains(runner.input, "analyze_media_with_vision") || strings.Contains(runner.input, `<visual-evidence schema="modlens-v2"`) {
		t.Fatalf("main runner did not receive first-party reanalysis guidance:\n%s", runner.input)
	}
}

func TestReanalysisCanReuseHistoricalToolImageData(t *testing.T) {
	sess := agent.NewSession("system")
	sess.Add(provider.Message{
		Role: provider.RoleTool, Name: provider.LocalOnlyToolName, ToolCallID: provider.LocalOnlyToolID,
		LocalOnly: true, Images: []string{"data:image/png;base64," + tinyPNG},
	})
	exec := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	c := New(Options{Executor: exec, ModelRef: "text/main", VisionModelRef: "vision/vl"})

	images, _, err := c.ResolveHistoricalVisionMedia(context.Background(), vision.MediaSelection{Index: -1})
	if err != nil || len(images) != 1 || images[0].DataURL == "" {
		t.Fatalf("historical tool images=%+v err=%v", images, err)
	}
}

func TestHistoricalAttachmentPathsRejectTraversal(t *testing.T) {
	for _, input := range []string{
		`.reasonix/attachments/../../secret.png`,
		`.reasonix\\attachments\\..\\..\\secret.png`,
	} {
		if got := historicalAttachmentPaths(input); len(got) != 0 {
			t.Fatalf("historicalAttachmentPaths(%q) = %v, want traversal rejected", input, got)
		}
	}
}

func TestVisionReanalysisIntentHandlesAffirmativeAndNegativeRequests(t *testing.T) {
	for _, input := range []string{
		"analyze that screenshot again",
		"please inspect the image one more time",
		"重新分析刚才的截图",
	} {
		if !visionReanalysisRequested(input) {
			t.Fatalf("visionReanalysisRequested(%q) = false, want true", input)
		}
	}
	for _, input := range []string{
		"do not reanalyze that image",
		"do not analyze that image again",
		"don't re-inspect the image",
		"don't ever reanalyze that image",
		"never inspect the image again",
		"never again inspect the image",
		"never, ever reanalyze that image",
		"do not, ever reanalyze that image",
		"never reanalyze that",
		"what does reanalyze mean?",
		"without reanalyzing the screenshot, summarize the old result",
		"without inspecting the screenshot again, summarize the old result",
		"不要重新分析图片",
		"为什么没有重新分析截图",
	} {
		if visionReanalysisRequested(input) {
			t.Fatalf("visionReanalysisRequested(%q) = true, want false", input)
		}
	}
}

func TestReanalysisCanSelectAllHistoricalImages(t *testing.T) {
	sess := agent.NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleTool, Images: []string{"data:image/png;base64,first"}})
	sess.Add(provider.Message{Role: provider.RoleTool, Images: []string{"data:image/png;base64,second"}})
	sess.Add(provider.Message{Role: provider.RoleTool, Images: []string{"data:image/png;base64,third"}})
	exec := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	c := New(Options{Executor: exec, ModelRef: "text/main", VisionModelRef: "vision/vl"})

	images, _, err := c.ResolveHistoricalVisionMedia(context.Background(), vision.MediaSelection{All: true, Index: -1})
	if err != nil || len(images) != 3 || images[0].DataURL != "data:image/png;base64,first" || images[1].DataURL != "data:image/png;base64,second" || images[2].DataURL != "data:image/png;base64,third" {
		t.Fatalf("all historical images=%+v err=%v", images, err)
	}
}

func TestReanalysisCanSelectSecondHistoricalImage(t *testing.T) {
	sess := agent.NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleTool, Images: []string{"data:image/png;base64,first"}})
	sess.Add(provider.Message{Role: provider.RoleTool, Images: []string{"data:image/png;base64,second"}})
	sess.Add(provider.Message{Role: provider.RoleTool, Images: []string{"data:image/png;base64,third"}})
	exec := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	c := New(Options{Executor: exec, ModelRef: "text/main", VisionModelRef: "vision/vl"})

	images, _, err := c.ResolveHistoricalVisionMedia(context.Background(), vision.MediaSelection{Index: 1})
	if err != nil || len(images) != 1 || images[0].DataURL != "data:image/png;base64,second" {
		t.Fatalf("second historical image=%+v err=%v", images, err)
	}
}

func TestReanalysisDefersNoMediaDecisionToMainModelTool(t *testing.T) {
	c := New(Options{ModelRef: "text/main", VisionModelRef: "vision/vl"})
	media := c.resolveMediaForTurn("重新分析之前的图片")
	if !media.ReanalysisRequested || len(media.Images) != 0 {
		t.Fatalf("media resolution = %+v, want deferred reanalysis state", media)
	}
	input := media.applyReanalysisGuidance("重新分析之前的图片", true)
	if !strings.Contains(input, "<visual-reanalysis-request>") || !strings.Contains(input, "analyze_media_with_vision") {
		t.Fatalf("reanalysis tool guidance missing: %q", input)
	}
}

func TestHistoricalStoredImageWinsOverStaleTextPath(t *testing.T) {
	workspace := t.TempDir()
	writeImageRouteConfig(t, workspace)
	path := filepath.Join(workspace, "stale.png")
	if err := os.WriteFile(path, []byte("not an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess := agent.NewSession("system")
	sess.Add(provider.Message{
		Role: provider.RoleUser, Content: "inspect @stale.png",
		Images: []string{"data:image/png;base64,stored-bytes"},
	})
	exec := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	c := New(Options{Executor: exec, WorkspaceRoot: workspace, ModelRef: "text/main", VisionModelRef: "vision/vl"})

	images, _, err := c.ResolveHistoricalVisionMedia(context.Background(), vision.MediaSelection{Index: -1})
	if err != nil || len(images) != 1 || images[0].DataURL != "data:image/png;base64,stored-bytes" {
		t.Fatalf("stored historical image=%+v err=%v", images, err)
	}
}

func TestVisualModelAssistanceIsInjectedIntoMainModelUserTurn(t *testing.T) {
	workspace := t.TempDir()
	writeImageRouteConfig(t, workspace)
	runner := &capabilityRecordingRunner{}
	exec := agent.New(nil, tool.NewRegistry(), agent.NewSession("system"), agent.Options{}, event.Discard)
	c := New(Options{
		Runner: runner, Executor: exec, WorkspaceRoot: workspace,
		ModelRef: "text/main", VisionModelRef: "vision/vl",
	})
	beforeSystem := exec.Session().Snapshot()[0].Content

	if err := c.Run(context.Background(), "读取媒体工具返回的图片并完成任务"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Count(runner.input, "<visual-model-assistance") != 1 {
		t.Fatalf("main model input has %d visual assistance blocks:\n%s", strings.Count(runner.input, "<visual-model-assistance"), runner.input)
	}
	if !strings.Contains(runner.input, "vision/vl") || !strings.Contains(runner.input, "independent visual model") {
		t.Fatalf("main model input lacks independent visual model guidance:\n%s", runner.input)
	}
	if strings.Contains(strings.ToLower(runner.input), "mcp") || strings.Contains(runner.input, "Do not call") {
		t.Fatalf("visual assistance guidance should not mention or prohibit MCP:\n%s", runner.input)
	}
	if got := agent.StripTransientUserBlocks(runner.input); got != "读取媒体工具返回的图片并完成任务" {
		t.Fatalf("transient visual guidance leaked into user text: %q", got)
	}
	if got := exec.Session().Snapshot()[0].Content; got != beforeSystem {
		t.Fatalf("system prompt changed after visual guidance injection: %q -> %q", beforeSystem, got)
	}
}

func TestVisualModelAssistanceOnlyAdvertisesUsableFirstPartyTool(t *testing.T) {
	unavailable := (&Controller{visionModelRef: "vision/vl"}).injectVisualModelAssistance("reanalyze")
	if strings.Contains(unavailable, "analyze_media_with_vision") {
		t.Fatalf("unavailable vision tool was advertised: %q", unavailable)
	}
	available := (&Controller{visionModelRef: "vision/vl", visionDescriber: &routeEvidenceDescriber{}}).injectVisualModelAssistance("reanalyze")
	if !strings.Contains(available, "analyze_media_with_vision") {
		t.Fatalf("usable vision tool was not advertised: %q", available)
	}

	media := MediaTurnResolution{ReanalysisRequested: true}
	route := (&Controller{visionModelRef: "vision/vl"}).routeResolvedMediaOnce(context.Background(), &ImageRouteState{}, "reanalyze", "reanalyze", media)
	if strings.Contains(route.Input, "must call analyze_media_with_vision") || !strings.Contains(route.Input, "fresh visual analysis is unavailable") {
		t.Fatalf("unavailable reanalysis guidance is not truthful: %q", route.Input)
	}
}

func TestVisualEvidenceGuidanceUsesIndependentModelAsDefaultWithoutToolPolicy(t *testing.T) {
	c := &Controller{visionModelRef: "vision/vl"}
	guidance := c.injectVisualModelAssistance("用户问题")
	if !strings.Contains(guidance, "ordinary media") || !strings.Contains(guidance, "independent visual model") {
		t.Fatalf("guidance does not describe the default independent media path: %q", guidance)
	}
	evidence := joinVisualEvidenceInput("用户问题", `<visual-evidence schema="modlens-v2">evidence</visual-evidence>`)
	if strings.Contains(strings.ToLower(evidence), "mcp") || strings.Contains(evidence, "Do not call") {
		t.Fatalf("visual evidence guidance should not add a tool policy: %q", evidence)
	}
}

func TestVisualModelAssistancePrecedesCapabilityRoute(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(imageRouteTestTool{})
	c := New(Options{Registry: reg, VisionModelRef: "vision/vl"})
	composed := c.injectVisualModelAssistance("分析图片")
	got := c.withCapabilityRoute(context.Background(), composed, "use vision mcp to inspect the image")
	visual := strings.Index(got, "<visual-model-assistance")
	route := strings.Index(got, "<capability-route")
	if visual < 0 || route < 0 || visual > route {
		t.Fatalf("visual guidance must precede optional capability routing:\n%s", got)
	}
}

func TestVisualModelAssistanceLeavesMCPRouteCandidatesAvailable(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(imageRouteTestTool{})
	c := New(Options{Registry: reg, ModelRef: "text/main", VisionModelRef: "vision/vl"})
	decision := c.routeCapabilities(context.Background(), "use vision mcp to inspect the image")
	if len(decision.Candidates) != 1 || decision.Candidates[0].Entry.ID != "mcp-tool:vision/analyze_image" {
		t.Fatalf("MCP route candidates = %+v, want vision MCP candidate preserved", decision.Candidates)
	}
}

func TestVisualModelAssistanceIsNotSuppressedByUserTagText(t *testing.T) {
	c := New(Options{VisionModelRef: "vision/vl"})
	input := "请解释 <visual-model-assistance> 这个标签"
	got := c.injectVisualModelAssistance(input)
	if !strings.HasPrefix(got, `<visual-model-assistance version="1">`) {
		t.Fatalf("host visual assistance was not injected: %q", got)
	}
	if !strings.Contains(got, input) {
		t.Fatalf("user tag text was not preserved: %q", got)
	}
}

type imageRouteTestTool struct{}

func (imageRouteTestTool) Name() string                                             { return "mcp__vision__analyze_image" }
func (imageRouteTestTool) Description() string                                      { return "analyze image" }
func (imageRouteTestTool) Schema() json.RawMessage                                  { return json.RawMessage(`{"type":"object"}`) }
func (imageRouteTestTool) Execute(context.Context, json.RawMessage) (string, error) { return "", nil }
func (imageRouteTestTool) ReadOnly() bool                                           { return true }
