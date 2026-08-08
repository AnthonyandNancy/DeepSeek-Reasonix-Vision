package vision

import (
	"context"
	"fmt"
	"strings"

	"reasonix/internal/event"
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
	Text     string
	Images   []string
	Success  bool
	Attempts int
	Debug    string
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
	if in.ModelSupportsImages {
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
		if p != nil {
			p.emitNotice(event.LevelWarn, "Tool images unavailable", fmt.Sprintf("Tool %s returned image content, but no usable vision evidence model is configured.", toolName))
		}
		return ToolImageOutput{Text: AppendToolImageStatusWithin(in.ToolText, toolName, maxText), Images: nil, Debug: "no vision evidence model configured"}
	}
	imgs := toToolVisionImages(in.Images, toolName)
	attempts := 0
	for attempts < p.maxAttempts {
		attempts++
		p.emitPhase(fmt.Sprintf("Extracting visual evidence from %s image(s) (%d/%d)", toolName, attempts, p.maxAttempts))
		ev, _, err := p.describer.DescribeToolImagesOnce(ctx, p.modelRef, ToolImageDescribeInput{ToolName: toolName, ToolText: truncateToolContext(in.ToolText, maxToolContextBytes), TaskContext: truncateToolContext(in.TaskContext, maxToolContextBytes), Images: imgs})
		if err == nil {
			evidence := RenderEvidenceContextWithin(ev, "tool:"+toolName, maxToolEvidenceBytes)
			final := appendBoundedToolBlock(in.ToolText, "\n\n", evidence, "\n", maxText)
			p.emitSuccessNotice(in, toolName, attempts, evidence, final)
			return ToolImageOutput{Text: final, Images: nil, Success: true, Attempts: attempts, Debug: evidence}
		}
		if ctx.Err() != nil {
			break
		}
	}
	p.emitNotice(event.LevelWarn, "Tool visual evidence extraction failed", fmt.Sprintf("Tool %s image content was not readable after %d/%d attempt(s).", toolName, attempts, p.maxAttempts))
	return ToolImageOutput{Text: AppendToolImageStatusWithin(in.ToolText, toolName, maxText), Images: nil, Attempts: attempts, Debug: fmt.Sprintf("vision evidence failed after %d attempt(s)", attempts)}
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

func (p *ProviderToolImageProcessor) emitPhase(text string) {
	if p != nil && p.sink != nil {
		p.sink.Emit(event.Event{Kind: event.Phase, Text: text, Source: event.UsageSourceVision})
	}
}
func (p *ProviderToolImageProcessor) emitNotice(level event.Level, text, detail string) {
	if p != nil && p.sink != nil {
		p.sink.Emit(event.Event{Kind: event.Notice, Level: level, Text: text, Detail: detail, Source: event.UsageSourceVision})
	}
}
func (p *ProviderToolImageProcessor) emitSuccessNotice(in ToolImageInput, toolName string, attempts int, evidence, final string) {
	if p == nil || p.sink == nil {
		return
	}
	model := p.modelRef
	if model == "" {
		model = "configured vision model"
	}
	p.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: fmt.Sprintf("Visual evidence ready: %s (%d/%d)", model, attempts, p.maxAttempts), ModelRef: model, Detail: "Source tool: " + toolName + "\n\n" + truncateToolContext(evidence, maxToolEvidenceBytes) + "\n\nFinal tool text:\n" + truncateToolContext(final, maxToolEvidenceBytes*2), Source: event.UsageSourceVision})
}
