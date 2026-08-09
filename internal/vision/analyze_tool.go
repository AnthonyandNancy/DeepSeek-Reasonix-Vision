package vision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

const analyzeMediaToolName = "analyze_media_with_vision"
const maxAnalyzeInstructionBytes = 4 * 1024
const maxAnalyzeEvidenceBytes = 48 * 1024

type MediaSelection struct {
	All   bool
	Index int
}

type MediaResolver func(context.Context, MediaSelection) ([]Image, []string, error)
type InvocationIDResolver func(context.Context) string

type analyzeMediaTool struct {
	modelRef  string
	describer Describer
	resolve   MediaResolver
	ownerID   InvocationIDResolver
}

func NewAnalyzeMediaTool(modelRef string, describer Describer, resolve MediaResolver, ownerID InvocationIDResolver) tool.Tool {
	return &analyzeMediaTool{modelRef: strings.TrimSpace(modelRef), describer: describer, resolve: resolve, ownerID: ownerID}
}

func (*analyzeMediaTool) Name() string { return analyzeMediaToolName }

func (*analyzeMediaTool) Description() string {
	return "Analyze image media already stored in the current conversation with the application's independent visual model. Use this for a fresh reading of historical conversation media. Select the latest image by default, all images, or one one-based image index. This tool does not accept filesystem paths."
}

func (*analyzeMediaTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "selection":{"type":"string","enum":["latest","all"],"description":"Which conversation media to analyze. Defaults to latest."},
    "image_index":{"type":"integer","minimum":1,"description":"One-based image index across conversation media. Mutually exclusive with selection=all."},
    "instruction":{"type":"string","description":"Optional focus for the visual evidence extraction; it is not treated as image content."}
  },
  "additionalProperties":false
}`)
}

func (*analyzeMediaTool) ReadOnly() bool { return true }

func (t *analyzeMediaTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	result, err := t.ExecuteWithTranscriptMetadata(ctx, args)
	return result.Output, err
}

func (t *analyzeMediaTool) ExecuteWithTranscriptMetadata(ctx context.Context, args json.RawMessage) (tool.TranscriptMetadataResult, error) {
	selection, instruction, err := parseAnalyzeMediaArgs(args)
	if err != nil {
		return tool.TranscriptMetadataResult{}, err
	}
	if t == nil || t.describer == nil || t.resolve == nil || t.modelRef == "" {
		return tool.TranscriptMetadataResult{}, errors.New("analyze_media_with_vision: independent visual model is unavailable")
	}
	images, refs, err := t.resolve(ctx, selection)
	if err != nil {
		return tool.TranscriptMetadataResult{}, fmt.Errorf("analyze_media_with_vision: resolve conversation media: %w", err)
	}
	if len(images) == 0 {
		return tool.TranscriptMetadataResult{}, errors.New("analyze_media_with_vision: no conversation media is available for analysis")
	}
	if len(refs) == 0 {
		refs = imageRefs(images)
	}

	analysisID := NewAnalysisID()
	recorder := NewAnalysisRecorder(analysisID, AnalysisInitiatorMainModelTool, t.modelRef, refs, len(images))
	ownerID := ""
	if t.ownerID != nil {
		ownerID = strings.TrimSpace(t.ownerID(ctx))
	}
	analysisCtx := WithProgressScope(ctx, ProgressScope{
		AnalysisID: analysisID, Initiator: AnalysisInitiatorMainModelTool,
		OwnerKind: event.VisionOwnerTool, OwnerID: ownerID,
		MediaCount: len(images), Observe: recorder.Observe,
	})
	emitsProgress := DescriberEmitsVisionProgress(t.describer)
	if !emitsProgress {
		EmitProgress(analysisCtx, nil, event.VisionProgressInfo{Stage: event.VisionStagePreparing, ModelRef: t.modelRef})
	}
	evidence, _, describeErr := t.describer.DescribeOnce(analysisCtx, t.modelRef, images, instruction)
	if describeErr != nil {
		if !emitsProgress {
			stage := event.VisionStageFailed
			if errors.Is(describeErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
				stage = event.VisionStageCancelled
			}
			EmitProgress(analysisCtx, nil, event.VisionProgressInfo{Stage: stage, ModelRef: t.modelRef, Detail: visionFailureDetail(describeErr)})
		}
		return tool.TranscriptMetadataResult{
			VisualAnalyses: []provider.VisualAnalysisRecord{recorder.Snapshot(Evidence{}, "")},
		}, describeErr
	}
	validated, err := revalidateEvidence(evidence)
	if err != nil {
		EmitProgress(analysisCtx, nil, event.VisionProgressInfo{Stage: event.VisionStageFailed, ModelRef: t.modelRef, Detail: "invalid_modlens_output"})
		return tool.TranscriptMetadataResult{
			VisualAnalyses: []provider.VisualAnalysisRecord{recorder.Snapshot(Evidence{}, "")},
		}, fmt.Errorf("analyze_media_with_vision: invalid ModLens v2 evidence: %w", err)
	}
	if !emitsProgress {
		EmitProgress(analysisCtx, nil, event.VisionProgressInfo{Stage: event.VisionStageReady, ModelRef: t.modelRef})
	}
	rendered := RenderEvidenceContextWithin(validated, "conversation-media", maxAnalyzeEvidenceBytes)
	return tool.TranscriptMetadataResult{
		Output:         rendered,
		VisualAnalyses: []provider.VisualAnalysisRecord{recorder.Snapshot(validated, rendered)},
	}, nil
}

type analyzeMediaArgs struct {
	Selection   string `json:"selection"`
	ImageIndex  *int   `json:"image_index"`
	Instruction string `json:"instruction"`
}

func parseAnalyzeMediaArgs(raw json.RawMessage) (MediaSelection, string, error) {
	var args analyzeMediaArgs
	if len(strings.TrimSpace(string(raw))) == 0 {
		raw = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return MediaSelection{}, "", fmt.Errorf("analyze_media_with_vision: invalid arguments: %w", err)
	}
	selection := strings.ToLower(strings.TrimSpace(args.Selection))
	if selection == "" {
		selection = "latest"
	}
	if selection != "latest" && selection != "all" {
		return MediaSelection{}, "", fmt.Errorf("analyze_media_with_vision: selection must be latest or all")
	}
	if selection == "all" && args.ImageIndex != nil {
		return MediaSelection{}, "", errors.New("analyze_media_with_vision: selection=all and image_index are mutually exclusive")
	}
	out := MediaSelection{All: selection == "all", Index: -1}
	if args.ImageIndex != nil {
		if *args.ImageIndex < 1 {
			return MediaSelection{}, "", errors.New("analyze_media_with_vision: image_index must be one or greater")
		}
		out.All = false
		out.Index = *args.ImageIndex - 1
	}
	return out, truncateAnalysisText(strings.TrimSpace(args.Instruction), maxAnalyzeInstructionBytes), nil
}

func revalidateEvidence(evidence Evidence) (Evidence, error) {
	raw, err := json.Marshal(evidence)
	if err != nil {
		return Evidence{}, err
	}
	return ParseEvidence(string(raw))
}

func imageRefs(images []Image) []string {
	refs := make([]string, 0, len(images))
	for _, image := range images {
		ref := strings.TrimSpace(image.Ref)
		if ref == "" {
			ref = strings.TrimSpace(image.Path)
		}
		if ref != "" {
			refs = append(refs, ref)
		}
	}
	return refs
}
