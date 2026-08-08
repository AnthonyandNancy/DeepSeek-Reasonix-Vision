package control

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/config"
)

func writeVisionTestConfig(t *testing.T, root string) {
	t.Helper()
	cfg := config.Default()
	cfg.DefaultModel = "custom/vision-pro"
	cfg.Providers = []config.ProviderEntry{{
		Name:         "custom",
		Kind:         "openai",
		BaseURL:      "https://example.invalid/v1",
		Models:       []string{"text-only", "vision-pro"},
		VisionModels: []string{"vision-pro"},
	}}
	if err := cfg.SaveTo(filepath.Join(root, "reasonix.toml")); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

func TestControllerInputImagesResolvesAttachment(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeVisionTestConfig(t, dir)
	ref, err := SaveImageDataURL("data:image/png;base64," + tinyPNG)
	if err != nil {
		t.Fatalf("SaveImageDataURL: %v", err)
	}
	urls := (&Controller{modelRef: "custom/vision-pro"}).inputImages("look at @" + ref)
	if len(urls) != 1 {
		t.Fatalf("inputImages = %v, want one resolved data URL", urls)
	}
	if !strings.HasPrefix(urls[0], "data:image/png;base64,") {
		t.Errorf("resolved url = %q, want a png data URL", urls[0])
	}
}

func TestControllerInputImagesIgnoresNonAttachmentRefs(t *testing.T) {
	t.Chdir(t.TempDir())
	if urls := New(Options{}).inputImages("plain text with @missing.png"); len(urls) != 0 {
		t.Errorf("inputImages = %v, want none for a non-existent / non-attachment ref", urls)
	}
}

func TestControllerInputImagesResolvesWorkspaceImage(t *testing.T) {
	workspace := t.TempDir()
	writeVisionTestConfig(t, workspace)
	path := filepath.Join(workspace, "docs", "diagram.png")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, mustBase64(t, tinyPNG), 0o644); err != nil {
		t.Fatal(err)
	}

	urls := (&Controller{workspaceRoot: workspace, modelRef: "custom/vision-pro"}).inputImages("look at @docs/diagram.png")
	if len(urls) != 1 {
		t.Fatalf("inputImages = %v, want one resolved data URL", urls)
	}
	if !strings.HasPrefix(urls[0], "data:image/png;base64,") {
		t.Errorf("resolved url = %q, want a png data URL", urls[0])
	}
}

func TestControllerInputImagesResolvesAbsoluteWorkspaceImage(t *testing.T) {
	workspace := t.TempDir()
	writeVisionTestConfig(t, workspace)
	path := filepath.Join(workspace, "diagram.png")
	if err := os.WriteFile(path, mustBase64(t, tinyPNG), 0o644); err != nil {
		t.Fatal(err)
	}

	urls := (&Controller{workspaceRoot: workspace, modelRef: "custom/vision-pro"}).inputImages("look at @" + path)
	if len(urls) != 1 {
		t.Fatalf("inputImages = %v, want one resolved data URL", urls)
	}
	if !strings.HasPrefix(urls[0], "data:image/png;base64,") {
		t.Errorf("resolved url = %q, want a png data URL", urls[0])
	}
}

func TestControllerInputImagesRequiresWorkspaceForFileImageRefs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "diagram.png")
	if err := os.WriteFile(path, mustBase64(t, tinyPNG), 0o644); err != nil {
		t.Fatal(err)
	}

	urls := New(Options{}).inputImages("look at @" + path)
	if len(urls) != 0 {
		t.Fatalf("inputImages without a workspace = %v, want no file image refs", urls)
	}
}

func TestControllerInputImagesSkipsModelImagesWhenSelectedModelIsTextOnly(t *testing.T) {
	workspace := t.TempDir()
	cfg := config.Default()
	cfg.DefaultModel = "custom/text-only"
	cfg.Providers = []config.ProviderEntry{{
		Name:         "custom",
		Kind:         "openai",
		BaseURL:      "https://example.invalid/v1",
		Models:       []string{"text-only", "vision-pro"},
		VisionModels: []string{"vision-pro"},
	}}
	if err := cfg.SaveTo(filepath.Join(workspace, "reasonix.toml")); err != nil {
		t.Fatalf("save workspace config: %v", err)
	}
	path := filepath.Join(workspace, "diagram.png")
	if err := os.WriteFile(path, mustBase64(t, tinyPNG), 0o644); err != nil {
		t.Fatal(err)
	}

	c := &Controller{workspaceRoot: workspace, modelRef: "custom/text-only"}
	if urls := c.inputImages("look at @diagram.png"); len(urls) != 0 {
		t.Fatalf("text-only model should suppress image payloads, got %v", urls)
	}

	c.modelRef = "custom/vision-pro"
	if urls := c.inputImages("look at @diagram.png"); len(urls) != 1 {
		t.Fatalf("vision model should keep image payloads, got %v", urls)
	}
}

func TestControllerImageInputEnabledDoesNotFallbackFromUnknownRef(t *testing.T) {
	workspace := t.TempDir()
	writeVisionTestConfig(t, workspace)

	c := &Controller{workspaceRoot: workspace, modelRef: "deleted/model"}
	if c.imageInputEnabled() {
		t.Fatal("unknown ref should not inherit image input from the default fallback model")
	}
}

func TestResolveRefsMentionsConfiguredVisualEvidenceFallback(t *testing.T) {
	workspace := t.TempDir()
	cfg := config.Default()
	cfg.DefaultModel = "custom/text-only"
	cfg.Agent.VisionModel = "custom/vision-pro"
	cfg.Providers = []config.ProviderEntry{{
		Name:         "custom",
		Kind:         "openai",
		BaseURL:      "https://example.invalid/v1",
		Models:       []string{"text-only", "vision-pro"},
		VisionModels: []string{"vision-pro"},
	}}
	if err := cfg.SaveTo(filepath.Join(workspace, "reasonix.toml")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workspace, "diagram.png")
	if err := os.WriteFile(path, mustBase64(t, tinyPNG), 0o644); err != nil {
		t.Fatal(err)
	}

	c := &Controller{workspaceRoot: workspace, modelRef: "custom/text-only", visionModelRef: "custom/vision-pro"}
	block, errs := c.ResolveRefs(context.Background(), "look at @diagram.png")
	if len(errs) != 0 {
		t.Fatalf("ResolveRefs errors = %v", errs)
	}
	if !strings.Contains(block, "configured vision evidence model") || !strings.Contains(block, "ModLens v2") {
		t.Fatalf("image ref note does not describe the visual-evidence fallback:\n%s", block)
	}
	if strings.Contains(block, "Text-only models can still use an available OCR/image/vision tool") {
		t.Fatalf("fallback note should not encourage a duplicate vision tool call:\n%s", block)
	}
}

func TestResolveInputImagesReadsWorkspaceAttachmentOutsideProcessCWD(t *testing.T) {
	workspace := t.TempDir()
	writeVisionTestConfig(t, workspace)
	ref, err := SaveImageBytesInRoot(workspace, "image/png", mustBase64(t, tinyPNG))
	if err != nil {
		t.Fatalf("SaveImageBytesInRoot: %v", err)
	}
	t.Chdir(t.TempDir())

	c := &Controller{workspaceRoot: workspace, modelRef: "custom/text-only"}
	images := c.resolveInputImages("look at @" + ref)
	if len(images) != 1 || images[0].Error != "" || !strings.HasPrefix(images[0].DataURL, "data:image/png;base64,") {
		t.Fatalf("resolved workspace attachment = %+v, want one readable image", images)
	}
}
