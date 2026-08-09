package control

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/vision"
)

const (
	maxVisionAttemptsPerTurn   = 3
	maxUserVisionEvidenceBytes = 48 * 1024
)

const directUserImageEvidenceReady = `<direct-visual-input-status>
The attached user image(s) were already analyzed by the configured independent visual model.
Use the ModLens v2 evidence below as visual context for this turn.
The host owns ordinary media analysis for this turn; do not infer pixels from a filename, path, or metadata.
This is the fresh analysis for the attached image(s); do not call analyze_media_with_vision for the attached image(s) in this turn.
</direct-visual-input-status>`

func visualModelAssistanceBlock(modelRef string, firstPartyTool bool) string {
	toolGuidance := ""
	if firstPartyTool {
		toolGuidance = "\nFor a fresh analysis of media already stored in the conversation, call analyze_media_with_vision before answering."
	}
	return fmt.Sprintf(`<visual-model-assistance version="1">
The application has a configured independent visual model: %s.
For ordinary media, the host routes image bytes to this model before the main model reasons.
When an application media tool returns image data, the host sends that media to the independent visual model for analysis.%s
Use the resulting ModLens v2 visual evidence as the visual context for this task; do not infer pixels from a filename or path.
</visual-model-assistance>`, strings.TrimSpace(modelRef), toolGuidance)
}

func (c *Controller) injectVisualModelAssistance(input string) string {
	return c.injectVisualModelAssistanceWithTool(input, c != nil && c.visionDescriber != nil)
}

func (c *Controller) injectVisualModelAssistanceWithTool(input string, firstPartyTool bool) string {
	if c == nil || c.visionModelRefOr() == "" {
		return input
	}
	block := visualModelAssistanceBlock(c.visionModelRefOr(), firstPartyTool)
	if strings.TrimSpace(input) == "" {
		return block
	}
	return block + "\n\n" + input
}

type ImageRouteMode uint8

const (
	ImageRouteNone ImageRouteMode = iota
	ImageRouteDirectMain
	ImageRouteVisionEvidence
	ImageRoutePathOnly
)

type ImageRouteResult struct {
	Mode           ImageRouteMode
	Input          string
	Images         []string
	Notice         string
	VisionUsage    *provider.Usage
	VisualAnalyses []provider.VisualAnalysisRecord
}
type ImageRouteState struct {
	Resolved                 bool
	VisionAttempts           int
	RequireIndependentVision bool
}
type VisionModelStatusKind uint8

const (
	VisionModelNotConfigured VisionModelStatusKind = iota
	VisionModelUnavailable
	VisionModelUnsupported
	VisionModelSupported
)

type VisionModelStatus struct {
	Kind             VisionModelStatusKind
	ModelRef, Reason string
}

func (c *Controller) mainModelSupportsVision() bool { return c.imageInputEnabled() }
func (c *Controller) visionModelRefOr() string      { return strings.TrimSpace(c.visionModelRef) }

// VisionModelRef returns the provider/model reference for visual evidence.
func (c *Controller) VisionModelRef() string { return c.visionModelRef }

func (c *Controller) resolveVisionModelStatus() VisionModelStatus {
	ref := c.visionModelRefOr()
	if ref == "" {
		return VisionModelStatus{Kind: VisionModelNotConfigured}
	}
	cfg, err := config.LoadForRoot(c.workspaceRoot)
	if err == nil {
		if entry, ok := cfg.ResolveModel(ref); ok {
			if c.mainModelSupportsVision() && normalizeModelRef(cfg, c.modelRef) == normalizeModelRef(cfg, ref) {
				return VisionModelStatus{Kind: VisionModelUnavailable, ModelRef: ref, Reason: "vision model is the main model"}
			}
			if !config.EffectiveVision(entry) {
				return VisionModelStatus{Kind: VisionModelUnsupported, ModelRef: ref, Reason: "model is not configured for image input"}
			}
			return VisionModelStatus{Kind: VisionModelSupported, ModelRef: ref}
		}
	}
	// Boot can resolve models supplied by a runtime ProviderResolver even when
	// they do not exist in TOML. A wired describer is the authoritative signal
	// for that case; do not disable a valid resolver-backed visual bridge here.
	if c.visionDescriber != nil {
		return VisionModelStatus{Kind: VisionModelSupported, ModelRef: ref}
	}
	if err != nil {
		return VisionModelStatus{Kind: VisionModelUnavailable, ModelRef: ref, Reason: "config load failed"}
	}
	return VisionModelStatus{Kind: VisionModelUnavailable, ModelRef: ref, Reason: "unknown model reference"}
}
func normalizeModelRef(cfg *config.Config, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if entry, ok := cfg.ResolveModel(ref); ok {
		return entry.Name + "/" + entry.Model
	}
	return ref
}

func (c *Controller) routeImagesOnce(ctx context.Context, state *ImageRouteState, input, rawQuestion string, images []ResolvedImage) ImageRouteResult {
	if state == nil {
		state = &ImageRouteState{}
	}
	if state.Resolved {
		return pathOnlyResult(input, images, "image routing already completed for this turn")
	}
	if len(images) == 0 {
		state.Resolved = true
		return ImageRouteResult{Mode: ImageRouteNone, Input: input}
	}
	if hasImageResolutionFailure(images) {
		state.Resolved = true
		return pathOnlyResult(input, images, "one or more images were unreadable or unsupported")
	}
	status := c.resolveVisionModelStatus()
	if status.Kind == VisionModelSupported && c.visionDescriber != nil {
		visionImages := toVisionImages(images)
		analysisID := vision.NewAnalysisID()
		recorder := vision.NewAnalysisRecorder(
			analysisID, vision.AnalysisInitiatorHostAuto, status.ModelRef,
			mediaRefsForResolvedImages(images), len(visionImages),
		)
		analysisCtx := vision.WithProgressScope(ctx, vision.ProgressScope{
			AnalysisID: analysisID,
			Initiator:  vision.AnalysisInitiatorHostAuto, OwnerKind: event.VisionOwnerUser,
			MediaCount: len(visionImages), Observe: recorder.Observe,
		})
		emitsProgress := vision.DescriberEmitsVisionProgress(c.visionDescriber)
		for state.VisionAttempts < maxVisionAttemptsPerTurn {
			state.VisionAttempts++
			if !emitsProgress {
				vision.EmitProgress(analysisCtx, c.sink, event.VisionProgressInfo{Stage: event.VisionStagePreparing, ModelRef: status.ModelRef})
			}
			ev, usage, err := c.visionDescriber.DescribeOnce(analysisCtx, status.ModelRef, visionImages, rawQuestion)
			if err == nil {
				if !emitsProgress {
					vision.EmitProgress(analysisCtx, c.sink, event.VisionProgressInfo{Stage: event.VisionStageReady, ModelRef: status.ModelRef})
				}
				state.Resolved = true
				evidence := vision.RenderEvidenceContextWithin(ev, "user-attachment", maxUserVisionEvidenceBytes)
				final := joinVisualEvidenceInput(stripResolvedUserImageContext(input, images), evidence)
				return ImageRouteResult{
					Mode: ImageRouteVisionEvidence, Input: final, Images: nil, VisionUsage: usage,
					VisualAnalyses: []provider.VisualAnalysisRecord{recorder.Snapshot(ev, evidence)},
				}
			}
			if !emitsProgress {
				stage := event.VisionStageFailed
				if ctx.Err() != nil {
					stage = event.VisionStageCancelled
				}
				vision.EmitProgress(analysisCtx, c.sink, event.VisionProgressInfo{Stage: stage, ModelRef: status.ModelRef, Detail: "request_failed"})
			}
			if ctx.Err() != nil {
				break
			}
		}
		analysis := []provider.VisualAnalysisRecord{recorder.Snapshot(vision.Evidence{}, "")}
		if ctx.Err() != nil || state.VisionAttempts >= maxVisionAttemptsPerTurn {
			state.Resolved = true
			if c.mainModelSupportsVision() && !state.RequireIndependentVision {
				return ImageRouteResult{Mode: ImageRouteDirectMain, Input: input, Images: imageDataURLs(images), Notice: "visual evidence extraction failed; using the vision-capable main model", VisualAnalyses: analysis}
			}
			result := pathOnlyResult(input, images, fmt.Sprintf("visual evidence extraction failed after %d attempt(s)", state.VisionAttempts))
			result.VisualAnalyses = analysis
			return result
		}
	}
	if c.mainModelSupportsVision() && !state.RequireIndependentVision {
		state.Resolved = true
		return ImageRouteResult{Mode: ImageRouteDirectMain, Input: input, Images: imageDataURLs(images)}
	}
	switch status.Kind {
	case VisionModelNotConfigured:
		return markPathOnly(state, input, images, "the main model is text-only and no vision evidence model is configured")
	case VisionModelUnavailable:
		return markPathOnly(state, input, images, "the configured vision evidence model is unavailable")
	case VisionModelUnsupported:
		return markPathOnly(state, input, images, fmt.Sprintf("the configured vision evidence model %q is not marked as image-capable", status.ModelRef))
	default:
		return markPathOnly(state, input, images, "the vision evidence extractor is not wired")
	}
}

func markPathOnly(state *ImageRouteState, input string, images []ResolvedImage, notice string) ImageRouteResult {
	state.Resolved = true
	return pathOnlyResult(input, images, notice)
}

func joinVisualEvidenceInput(input, evidence string) string {
	parts := make([]string, 0, 3)
	if input = strings.TrimSpace(input); input != "" {
		parts = append(parts, input)
	}
	parts = append(parts, directUserImageEvidenceReady, strings.TrimSpace(evidence))
	return strings.Join(parts, "\n\n") + "\n"
}

func stripResolvedUserImageContext(input string, images []ResolvedImage) string {
	cleaned := input
	for _, image := range images {
		ref := strings.TrimSpace(image.Ref)
		if ref != "" {
			if !strings.HasPrefix(ref, "@") {
				ref = "@" + ref
			}
			cleaned = strings.ReplaceAll(cleaned, ref, "")
		}
		path := strings.TrimSpace(filepath.ToSlash(image.Path))
		if path == "" {
			continue
		}
		start := strings.Index(cleaned, `<image path="`+path+`">`)
		if start < 0 {
			continue
		}
		end := strings.Index(cleaned[start:], "</image>")
		if end < 0 {
			continue
		}
		end += start + len("</image>")
		cleaned = cleaned[:start] + cleaned[end:]
	}
	return strings.TrimSpace(cleaned)
}

func pathOnlyResult(input string, images []ResolvedImage, notice string) ImageRouteResult {
	return ImageRouteResult{Mode: ImageRoutePathOnly, Input: injectImageUnavailableContext(input, images, notice), Notice: notice}
}
func injectImageUnavailableContext(input string, images []ResolvedImage, notice string) string {
	var b strings.Builder
	b.WriteString("\n\n<image-processing-status>\nThe user attached image(s), but the current model chain could not read their pixels:\n")
	for _, img := range images {
		name := filepath.Base(img.Path)
		if name == "." || name == "" {
			name = img.Ref
		}
		fmt.Fprintf(&b, "- %s\n", name)
	}
	fmt.Fprintf(&b, "Reason: %s.\n", notice)
	b.WriteString("Do not claim to have seen the image content. If the task depends on pixels, state that the image could not be read.\n</image-processing-status>\n")
	return input + b.String()
}
func (c *Controller) emitImageRouteNotice(notice string) {
	if notice != "" && c.sink != nil {
		c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: notice})
	}
}
func imageDataURLs(images []ResolvedImage) []string {
	out := make([]string, 0, len(images))
	for _, img := range images {
		if img.DataURL != "" {
			out = append(out, img.DataURL)
		}
	}
	return out
}
func toVisionImages(images []ResolvedImage) []vision.Image {
	out := make([]vision.Image, 0, len(images))
	for _, img := range images {
		if img.DataURL != "" {
			out = append(out, vision.Image{Ref: img.Ref, Path: img.Path, DataURL: img.DataURL})
		}
	}
	return out
}
func hasImageResolutionFailure(images []ResolvedImage) bool {
	for _, img := range images {
		if img.Error != "" || img.DataURL == "" {
			return true
		}
	}
	return false
}
