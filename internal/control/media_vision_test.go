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
		"重新分析这个问题",
		"重新分析这段代码",
		"reanalyze this bug",
		"analyze the API response again",
	} {
		if visionReanalysisRequested(input) {
			t.Fatalf("visionReanalysisRequested(%q) = true, want false", input)
		}
	}
}

func TestBareVisionReanalysisRequiresHistoricalMedia(t *testing.T) {
	empty := New(Options{ModelRef: "text/main", VisionModelRef: "vision/vl"})
	if got := empty.resolveMediaForTurn("重新分析"); got.ReanalysisRequested {
		t.Fatalf("empty conversation treated bare reanalysis as visual: %+v", got)
	}

	sess := agent.NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "之前的图片", Images: []string{"data:image/png;base64," + tinyPNG}})
	exec := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	withMedia := New(Options{Executor: exec, ModelRef: "text/main", VisionModelRef: "vision/vl"})
	if got := withMedia.resolveMediaForTurn("重新分析"); !got.ReanalysisRequested {
		t.Fatalf("historical media did not activate bare visual reanalysis: %+v", got)
	}
}

func TestHistoricalReanalysisRoutesLatestImageToVisionCapableMainModel(t *testing.T) {
	workspace := t.TempDir()
	writeVisionCapableImageRouteConfig(t, workspace)
	sess := agent.NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "older image", Images: []string{"data:image/png;base64,older"}})
	sess.Add(provider.Message{Role: provider.RoleTool, LocalOnly: true, Images: []string{"data:image/png;base64," + tinyPNG}})
	exec := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	d := &routeEvidenceDescriber{}
	c := New(Options{
		Executor: exec, WorkspaceRoot: workspace, ModelRef: "main/vl", VisionModelRef: "vision/evidence",
		VisionDescriber: d,
	})

	media := c.resolveMediaForTurn("重新分析")
	route := c.routeResolvedMediaOnce(context.Background(), &ImageRouteState{}, "重新分析", "重新分析", media)
	if !media.ReanalysisRequested || len(media.Images) != 1 || media.Images[0].DataURL != "data:image/png;base64,"+tinyPNG || route.Mode != ImageRouteDirectMain || len(route.Images) != 1 || route.Images[0] != "data:image/png;base64,"+tinyPNG || d.calls != 0 {
		t.Fatalf("media=%+v route=%+v calls=%d", media, route, d.calls)
	}
	if strings.Contains(route.Input, "<visual-model-assistance>") || strings.Contains(route.Input, "<visual-reanalysis-request>") {
		t.Fatalf("historical reanalysis injected independent-vision guidance: %q", route.Input)
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

// A media-free turn no longer advertises the visual bridge, so the injection
// mechanics ride an explicit reanalysis request instead: exactly one block,
// strippable for the UI, and never folded into the cache-stable prefix.
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

	if err := c.Run(context.Background(), "重新分析这张图片"); err != nil {
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
	if got := agent.StripTransientUserBlocks(runner.input); got != "重新分析这张图片" {
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

func TestVisualModelAssistanceSkipsVisionCapableMainModel(t *testing.T) {
	workspace := t.TempDir()
	writeVisionCapableImageRouteConfig(t, workspace)
	c := &Controller{
		workspaceRoot: workspace, modelRef: "main/vl", visionModelRef: "vision/evidence",
		visionDescriber: &routeEvidenceDescriber{},
	}
	input := "reanalyze the image"
	if got := c.injectVisualModelAssistanceWithTool(input, true); got != input {
		t.Fatalf("vision-capable main model received independent visual guidance: %q", got)
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

// The host states what it already did; it never forbids a visual tool. The
// evidence block must not invite a redundant historical re-read, but the main
// model stays free to request another reading when the evidence falls short.
func TestFreshUserImageEvidenceDoesNotInviteDuplicateFirstPartyAnalysis(t *testing.T) {
	workspace := t.TempDir()
	writeImageRouteConfig(t, workspace)
	c := New(Options{
		WorkspaceRoot: workspace, ModelRef: "text/main", VisionModelRef: "vision/vl",
		VisionDescriber: &routeEvidenceDescriber{},
	})
	route := c.routeResolvedMediaOnce(context.Background(), &ImageRouteState{}, "分析图片 @fresh.png", "分析图片", MediaTurnResolution{
		Images: []ResolvedImage{{Ref: "fresh.png", DataURL: "data:image/png;base64," + tinyPNG}},
	})
	if route.Mode != ImageRouteVisionEvidence {
		t.Fatalf("route mode = %v, want fresh independent visual evidence", route.Mode)
	}
	if strings.Contains(route.Input, "For a fresh analysis of media already stored in the conversation") {
		t.Fatalf("fresh attachment guidance invited a duplicate historical analysis:\n%s", route.Input)
	}
	if !strings.Contains(route.Input, "request another visual reading only when this evidence is insufficient") {
		t.Fatalf("fresh attachment guidance lost its sufficiency statement:\n%s", route.Input)
	}
	if strings.Contains(route.Input, "do not call") || strings.Contains(route.Input, "must call") {
		t.Fatalf("host guidance gate-kept a visual tool:\n%s", route.Input)
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

// Pasting an old vision conversation as plain text is not media. A standing
// analyze_media_with_vision line would push the text-only main model onto
// whichever visual tool still answers, and the first-party one fails without
// conversation media — so it lands on an MCP tool instead.
func TestPastedVisionProseTurnGetsNoVisualBridgeGuidance(t *testing.T) {
	workspace := t.TempDir()
	writeImageRouteConfig(t, workspace)
	c := New(Options{
		WorkspaceRoot: workspace, ModelRef: "text/main", VisionModelRef: "vision/vl",
		VisionDescriber: &routeEvidenceDescriber{},
	})
	pasted := "图中显示的是一辆理想 L9（Li Auto L9）——一辆深绿色的 SUV。\n" +
		"图中带有“豆包AI生成”的水印。\n" +
		`{"summary":"Screenshot of a Chinese messaging app","ocr":{"full_text":"理想L9"}}`

	media := c.resolveMediaForTurn(pasted)
	if len(media.Images) != 0 || media.ReanalysisRequested {
		t.Fatalf("pasted prose resolved as media: images=%d reanalysis=%v", len(media.Images), media.ReanalysisRequested)
	}
	route := c.routeResolvedMediaOnce(context.Background(), &ImageRouteState{}, pasted, pasted, media)
	if route.Input != pasted {
		t.Fatalf("media-free turn was rewritten:\n%s", route.Input)
	}
	if strings.Contains(route.Input, "analyze_media_with_vision") {
		t.Fatalf("media-free turn was told to call the visual tool:\n%s", route.Input)
	}
}

func TestUnrelatedTurnGetsNoVisualBridgeGuidance(t *testing.T) {
	workspace := t.TempDir()
	writeImageRouteConfig(t, workspace)
	c := New(Options{
		WorkspaceRoot: workspace, ModelRef: "text/main", VisionModelRef: "vision/vl",
		VisionDescriber: &routeEvidenceDescriber{},
	})
	input := "你好，帮我写个快速排序"
	route := c.routeResolvedMediaOnce(context.Background(), &ImageRouteState{}, input, input, c.resolveMediaForTurn(input))
	if strings.Contains(route.Input, "visual-model-assistance") {
		t.Fatalf("unrelated coding turn received visual guidance:\n%s", route.Input)
	}
}

// The bridge must still be announced when the turn actually carries media, so
// the main model reads the injected ModLens evidence as this turn's pixels.
func TestAttachedImageTurnKeepsVisualBridgeGuidance(t *testing.T) {
	workspace := t.TempDir()
	writeImageRouteConfig(t, workspace)
	c := New(Options{
		WorkspaceRoot: workspace, ModelRef: "text/main", VisionModelRef: "vision/vl",
		VisionDescriber: &routeEvidenceDescriber{},
	})
	route := c.routeResolvedMediaOnce(context.Background(), &ImageRouteState{}, "分析图片 @fresh.png", "分析图片", MediaTurnResolution{
		Images: []ResolvedImage{{Ref: "fresh.png", DataURL: "data:image/png;base64," + tinyPNG}},
	})
	if route.Mode != ImageRouteVisionEvidence {
		t.Fatalf("route mode = %v, want independent visual evidence", route.Mode)
	}
	if !strings.Contains(route.Input, "visual-model-assistance") {
		t.Fatalf("attachment turn lost visual guidance:\n%s", route.Input)
	}
}

// An explicit "reanalyze that image" request carries no attachment, so the tool
// path stays advertised even though resolveMediaForTurn found no bytes.
func TestExplicitReanalysisKeepsVisualToolGuidance(t *testing.T) {
	workspace := t.TempDir()
	writeImageRouteConfig(t, workspace)
	sess := agent.NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleUser, Images: []string{"data:image/png;base64," + tinyPNG}})
	exec := agent.New(nil, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	c := New(Options{
		Executor: exec, WorkspaceRoot: workspace, ModelRef: "text/main", VisionModelRef: "vision/vl",
		VisionDescriber: &routeEvidenceDescriber{},
	})
	input := "重新分析这张图片"
	media := c.resolveMediaForTurn(input)
	if !media.ReanalysisRequested {
		t.Fatalf("explicit reanalysis was not detected")
	}
	route := c.routeResolvedMediaOnce(context.Background(), &ImageRouteState{}, input, input, media)
	if !strings.Contains(route.Input, "analyze_media_with_vision") {
		t.Fatalf("explicit reanalysis lost the visual tool guidance:\n%s", route.Input)
	}
}

// Pasted desktop text keeps the @[label](path) render form. The host must
// resolve it to the same media as the plain @path submit form, or a pasted
// attachment silently stops being an image for the whole vision pipeline.
func TestPastedNamedAttachmentRefResolvesLikePlainRef(t *testing.T) {
	workspace := t.TempDir()
	writeImageRouteConfig(t, workspace)
	rel := filepath.ToSlash(filepath.Join(".reasonix", "attachments", "clipboard-20260810-150443.png"))
	path := filepath.Join(workspace, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, mustBase64(t, tinyPNG), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string]string{
		"named":      "这个图的汽车是什么 @[狐狸与阴阳师游戏结合的logo设计.png](" + rel + ")",
		"plain":      "这个图的汽车是什么 @" + rel,
		"named only": "@[shot.png](" + rel + ")",
	} {
		c := New(Options{
			WorkspaceRoot: workspace, ModelRef: "text/main", VisionModelRef: "vision/vl",
			VisionDescriber: &routeEvidenceDescriber{},
		})
		media := c.resolveMediaForTurn(input)
		if len(media.Images) != 1 || media.Images[0].DataURL == "" {
			t.Fatalf("[%s] resolved images = %+v, want one readable image", name, media.Images)
		}
		route := c.routeResolvedMediaOnce(context.Background(), &ImageRouteState{}, input, input, media)
		if route.Mode != ImageRouteVisionEvidence {
			t.Fatalf("[%s] route mode = %v, want independent visual evidence", name, route.Mode)
		}
	}
}

// A markdown link is prose, not an attachment.
func TestMarkdownLinkIsNotTreatedAsMedia(t *testing.T) {
	workspace := t.TempDir()
	writeImageRouteConfig(t, workspace)
	c := New(Options{
		WorkspaceRoot: workspace, ModelRef: "text/main", VisionModelRef: "vision/vl",
		VisionDescriber: &routeEvidenceDescriber{},
	})
	media := c.resolveMediaForTurn("参考 @[官网](https://example.com/a.png) 的说明")
	if len(media.Images) != 0 {
		t.Fatalf("markdown link resolved as media: %+v", media.Images)
	}
}

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

type imageRouteTestTool struct{}

func (imageRouteTestTool) Name() string                                             { return "mcp__vision__analyze_image" }
func (imageRouteTestTool) Description() string                                      { return "analyze image" }
func (imageRouteTestTool) Schema() json.RawMessage                                  { return json.RawMessage(`{"type":"object"}`) }
func (imageRouteTestTool) Execute(context.Context, json.RawMessage) (string, error) { return "", nil }
func (imageRouteTestTool) ReadOnly() bool                                           { return true }
