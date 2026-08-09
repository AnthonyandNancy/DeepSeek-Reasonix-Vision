package vision

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

const MaxToolImageAttempts = 3
const DefaultToolResultTextBytes = 32 * 1024
const maxToolEvidenceBytes = 24 * 1024
const maxToolContextBytes = 4 * 1024

type ToolImageInput struct {
	ToolName            string
	ToolCallID          string
	ToolText            string
	Images              []string
	ModelRef            string
	ModelSupportsImages bool
	TaskContext         string
	MaxTextBytes        int
}

type ToolImageOutput struct {
	Text           string
	Images         []string
	Success        bool
	Attempts       int
	Debug          string
	VisualAnalyses []provider.VisualAnalysisRecord
}
type ToolImageProcessor interface {
	ProcessToolImages(context.Context, ToolImageInput) ToolImageOutput
}

type ProviderToolImageProcessor struct {
	describer   Describer
	sink        event.Sink
	modelRef    string
	maxAttempts int
}

func NewToolImageProcessor(modelRef string, describer Describer, sink event.Sink) *ProviderToolImageProcessor {
	return &ProviderToolImageProcessor{describer: describer, sink: sink, modelRef: strings.TrimSpace(modelRef), maxAttempts: MaxToolImageAttempts}
}

func (p *ProviderToolImageProcessor) ProcessToolImages(ctx context.Context, in ToolImageInput) ToolImageOutput {
	if len(in.Images) == 0 {
		return ToolImageOutput{Text: in.ToolText, Images: in.Images}
	}
	toolName := strings.TrimSpace(in.ToolName)
	if toolName == "" {
		toolName = "unknown"
	}
	maxText := in.MaxTextBytes
	if maxText <= 0 {
		maxText = DefaultToolResultTextBytes
	}
	if p == nil || p.describer == nil || strings.TrimSpace(p.modelRef) == "" {
		if in.ModelSupportsImages {
			return ToolImageOutput{Text: in.ToolText, Images: in.Images}
		}
		if p != nil {
			p.emitProgress(ctx, event.VisionStageFailed, "model_unavailable")
		}
		return ToolImageOutput{Text: AppendToolImageStatusWithin(in.ToolText, toolName, maxText), Images: nil, Debug: "no vision evidence model configured"}
	}
	imgs := toToolVisionImages(in.Images, toolName)
	refs := make([]string, 0, len(imgs))
	for _, image := range imgs {
		refs = append(refs, image.Ref)
	}
	analysisID := NewAnalysisID()
	recorder := NewAnalysisRecorder(analysisID, AnalysisInitiatorToolMediaBridge, p.modelRef, refs, len(imgs))
	analysisCtx := WithProgressScope(ctx, ProgressScope{
		AnalysisID: analysisID, Initiator: AnalysisInitiatorToolMediaBridge, MediaCount: len(imgs), Observe: recorder.Observe,
	})
	attempts := 0
	var lastErr error
	for attempts < p.maxAttempts {
		attempts++
		if !DescriberEmitsVisionProgress(p.describer) {
			p.emitProgress(analysisCtx, event.VisionStagePreparing, "")
		}
		ev, _, err := p.describer.DescribeToolImagesOnce(analysisCtx, p.modelRef, ToolImageDescribeInput{ToolName: toolName, ToolText: truncateToolContext(in.ToolText, maxToolContextBytes), TaskContext: truncateToolContext(in.TaskContext, maxToolContextBytes), Images: imgs})
		if err == nil {
			evidence := RenderEvidenceContextWithin(ev, "tool:"+toolName, maxToolEvidenceBytes)
			final := appendBoundedToolBlock(in.ToolText, "\n\n", evidence, "\n", maxText)
			p.emitProgress(analysisCtx, event.VisionStageReady, "")
			return ToolImageOutput{
				Text: final, Images: nil, Success: true, Attempts: attempts, Debug: evidence,
				VisualAnalyses: []provider.VisualAnalysisRecord{recorder.Snapshot(ev, evidence)},
			}
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	detail := visionFailureDetail(lastErr)
	if ctx.Err() != nil {
		detail = visionFailureDetail(ctx.Err())
	}
	stage := event.VisionStageFailed
	if errors.Is(ctx.Err(), context.Canceled) || (errors.Is(lastErr, context.Canceled) && !errors.Is(lastErr, context.DeadlineExceeded)) {
		stage = event.VisionStageCancelled
	}
	p.emitProgress(analysisCtx, stage, detail)
	return ToolImageOutput{
		Text: AppendToolImageStatusWithin(in.ToolText, toolName, maxText), Images: nil, Attempts: attempts,
		Debug:          fmt.Sprintf("vision evidence failed after %d attempt(s)", attempts),
		VisualAnalyses: []provider.VisualAnalysisRecord{recorder.Snapshot(Evidence{}, "")},
	}
}

func AppendToolImageStatus(text, toolName string) string {
	return AppendToolImageStatusWithin(text, toolName, DefaultToolResultTextBytes)
}
func AppendToolImageStatusWithin(text, toolName string, maxBytes int) string {
	block := "\n\n<tool-image-status>\nTool " + safeEvidenceText(toolName) + " returned image content, but the current model chain could not read it.\nThe model must not claim that it saw the image.\n</tool-image-status>\n"
	return appendBoundedToolBlock(text, block, "", "", maxBytes)
}

func appendBoundedToolBlock(text, prefix, payload, suffix string, maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = DefaultToolResultTextBytes
	}
	if len(text)+len(prefix)+len(payload)+len(suffix) <= maxBytes {
		return text + prefix + payload + suffix
	}
	fixed := len(prefix) + len(suffix)
	if fixed >= maxBytes {
		return truncateToolSegment(prefix+payload+suffix, maxBytes)
	}
	available := maxBytes - fixed
	// Evidence/status blocks carry trusted host delimiters. Keep the payload
	// whole whenever it fits and spend the remaining budget on legacy tool text.
	if len(payload) <= available {
		textBudget := available - len(payload)
		return truncateToolSegment(text, textBudget) + prefix + payload + suffix
	}
	// An oversized payload is exceptional; bound it only after preserving the
	// final result ceiling. Callers should normally pre-bound structured blocks.
	return prefix + truncateToolSegment(payload, available) + suffix
}
func truncateToolSegment(text string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(text) <= maxBytes {
		return text
	}
	const marker = "\n……[content truncated]……\n"
	if maxBytes <= len(marker) {
		return toolSegmentPrefix(text, maxBytes)
	}
	remain := maxBytes - len(marker)
	head := remain / 2
	tail := remain - head
	return toolSegmentPrefix(text, head) + marker + toolSegmentSuffix(text, tail)
}
func toolSegmentPrefix(text string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(text) <= maxBytes {
		return text
	}
	cut := maxBytes
	for cut > 0 && text[cut]&0xc0 == 0x80 {
		cut--
	}
	return text[:cut]
}
func toolSegmentSuffix(text string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(text) <= maxBytes {
		return text
	}
	start := len(text) - maxBytes
	for start < len(text) && text[start]&0xc0 == 0x80 {
		start++
	}
	return text[start:]
}
func toToolVisionImages(urls []string, toolName string) []Image {
	out := make([]Image, 0, len(urls))
	for i, u := range urls {
		if u != "" {
			out = append(out, Image{Ref: fmt.Sprintf("tool:%s image %d", toolName, i+1), DataURL: u})
		}
	}
	return out
}
func truncateToolContext(text string, maxBytes int) string {
	if maxBytes <= 0 || len(text) <= maxBytes {
		return text
	}
	cut := maxBytes
	for cut > 0 && text[cut]&0xc0 == 0x80 {
		cut--
	}
	return text[:cut] + "\n……[content truncated]……"
}

func (p *ProviderToolImageProcessor) emitProgress(ctx context.Context, stage event.VisionProgressStage, detail string) {
	if p != nil {
		EmitProgress(ctx, p.sink, event.VisionProgressInfo{Stage: stage, ModelRef: p.modelRef, Detail: detail})
	}
}
