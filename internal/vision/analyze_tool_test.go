package vision

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	toolpkg "reasonix/internal/tool"
)

type analyzeToolDescriber struct {
	calls       int
	instruction string
	images      []Image
	evidence    Evidence
	err         error
}

type ownerAnalyzeToolDescriber struct {
	sink event.Sink
}

func (*ownerAnalyzeToolDescriber) EmitsVisionProgress() bool { return true }

func (d *ownerAnalyzeToolDescriber) DescribeOnce(ctx context.Context, _ string, _ []Image, _ string) (Evidence, *provider.Usage, error) {
	EmitProgress(ctx, d.sink, event.VisionProgressInfo{Stage: event.VisionStagePreparing})
	EmitProgress(ctx, d.sink, event.VisionProgressInfo{Stage: event.VisionStageReady})
	return analyzeToolEvidence(), nil, nil
}

func (*ownerAnalyzeToolDescriber) DescribeToolImagesOnce(context.Context, string, ToolImageDescribeInput) (Evidence, *provider.Usage, error) {
	panic("unexpected tool-image analysis")
}

func (d *analyzeToolDescriber) DescribeOnce(_ context.Context, _ string, images []Image, instruction string) (Evidence, *provider.Usage, error) {
	d.calls++
	d.instruction = instruction
	d.images = append([]Image(nil), images...)
	return d.evidence, nil, d.err
}

func (*analyzeToolDescriber) DescribeToolImagesOnce(context.Context, string, ToolImageDescribeInput) (Evidence, *provider.Usage, error) {
	panic("unexpected tool-image analysis")
}

func analyzeToolEvidence() Evidence {
	return Evidence{
		Summary:     "settings dialog",
		OCR:         OCR{FullText: "Save", Lines: []OCRLine{{Text: "Save"}}},
		Layout:      Layout{Regions: []LayoutRegion{{Type: "form", ReadingOrder: 1, Text: "Save"}}},
		Semantics:   Semantics{Scene: "desktop settings", Entities: []SemanticEntity{}, Relations: []SemanticRelation{}},
		Uncertainty: []string{"implementation details are not visible"},
	}
}

func TestAnalyzeMediaToolContract(t *testing.T) {
	got := NewAnalyzeMediaTool("vision/model", &analyzeToolDescriber{evidence: analyzeToolEvidence()}, func(context.Context, MediaSelection) ([]Image, []string, error) {
		return nil, nil, nil
	}, nil)
	if got.Name() != "analyze_media_with_vision" || !got.ReadOnly() {
		t.Fatalf("tool contract name=%q readOnly=%v", got.Name(), got.ReadOnly())
	}
	var schema map[string]any
	if err := json.Unmarshal(got.Schema(), &schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	properties, _ := schema["properties"].(map[string]any)
	selection, _ := properties["selection"].(map[string]any)
	if strings.Join(anyStrings(selection["enum"]), ",") != "latest,all" {
		t.Fatalf("selection enum = %#v", selection["enum"])
	}
	imageIndex, _ := properties["image_index"].(map[string]any)
	if imageIndex["minimum"] != float64(1) {
		t.Fatalf("image_index schema = %#v", imageIndex)
	}
	if _, ok := properties["instruction"]; !ok {
		t.Fatalf("instruction missing from schema: %#v", properties)
	}
}

func TestAnalyzeMediaToolRejectsAllWithImageIndex(t *testing.T) {
	got := NewAnalyzeMediaTool("vision/model", &analyzeToolDescriber{evidence: analyzeToolEvidence()}, func(context.Context, MediaSelection) ([]Image, []string, error) {
		return nil, nil, nil
	}, nil)
	if _, err := got.Execute(context.Background(), json.RawMessage(`{"selection":"all","image_index":2}`)); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error = %v, want mutually-exclusive validation", err)
	}
}

func TestAnalyzeMediaToolResolvesSelectionAndReturnsTranscriptMetadata(t *testing.T) {
	tests := []struct {
		name string
		args string
		want MediaSelection
	}{
		{name: "latest default", args: `{"instruction":"read the labels"}`, want: MediaSelection{Index: -1}},
		{name: "all", args: `{"selection":"all"}`, want: MediaSelection{All: true, Index: -1}},
		{name: "one based index", args: `{"image_index":2}`, want: MediaSelection{Index: 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var selection MediaSelection
			d := &analyzeToolDescriber{evidence: analyzeToolEvidence()}
			got := NewAnalyzeMediaTool("vision/model", d, func(_ context.Context, in MediaSelection) ([]Image, []string, error) {
				selection = in
				return []Image{{Ref: "history:1", DataURL: "data:image/png;base64,AA=="}}, []string{".reasonix/attachments/shot.png"}, nil
			}, nil)
			executor, ok := got.(toolpkg.TranscriptMetadataExecutor)
			if !ok {
				t.Fatalf("tool does not implement TranscriptMetadataExecutor: %T", got)
			}
			result, err := executor.ExecuteWithTranscriptMetadata(context.Background(), json.RawMessage(tc.args))
			if err != nil {
				t.Fatalf("ExecuteWithTranscriptMetadata: %v", err)
			}
			if selection != tc.want {
				t.Fatalf("selection=%+v want %+v", selection, tc.want)
			}
			if !strings.Contains(result.Output, `schema="modlens-v2"`) || !strings.Contains(result.Output, "settings dialog") {
				t.Fatalf("model-visible output is not ModLens v2: %s", result.Output)
			}
			if len(result.VisualAnalyses) != 1 {
				t.Fatalf("visual analyses = %+v", result.VisualAnalyses)
			}
			record := result.VisualAnalyses[0]
			if record.ID == "" || record.Initiator != AnalysisInitiatorMainModelTool || record.ModelRef != "vision/model" || record.Status != string(event.VisionStageReady) {
				t.Fatalf("record identity/status = %+v", record)
			}
			if len(record.MediaRefs) != 1 || record.MediaRefs[0] != ".reasonix/attachments/shot.png" || record.Summary != "settings dialog" || record.OCRText != "Save" {
				t.Fatalf("record evidence/media = %+v", record)
			}
			if d.calls != 1 || len(d.images) != 1 {
				t.Fatalf("describer calls=%d images=%+v", d.calls, d.images)
			}
			if tc.name == "latest default" && d.instruction != "read the labels" {
				t.Fatalf("instruction = %q", d.instruction)
			}
		})
	}
}

func TestAnalyzeMediaToolReturnsNoMediaErrorWithoutCallingDescriber(t *testing.T) {
	d := &analyzeToolDescriber{evidence: analyzeToolEvidence()}
	got := NewAnalyzeMediaTool("vision/model", d, func(context.Context, MediaSelection) ([]Image, []string, error) {
		return nil, nil, nil
	}, nil)
	if _, err := got.Execute(context.Background(), json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "no conversation media") {
		t.Fatalf("error = %v, want no conversation media", err)
	}
	if d.calls != 0 {
		t.Fatalf("describer called %d times without media", d.calls)
	}
}

func TestAnalyzeMediaToolProgressCarriesExecutingToolOwner(t *testing.T) {
	var events []event.Event
	sink := event.FuncSink(func(e event.Event) { events = append(events, e) })
	got := NewAnalyzeMediaTool("vision/model", &ownerAnalyzeToolDescriber{sink: sink}, func(context.Context, MediaSelection) ([]Image, []string, error) {
		return []Image{{DataURL: "data:image/png;base64,AA=="}}, []string{"shot.png"}, nil
	}, func(context.Context) string { return "call-vision" })
	executor := got.(toolpkg.TranscriptMetadataExecutor)
	if _, err := executor.ExecuteWithTranscriptMetadata(context.Background(), json.RawMessage(`{}`)); err != nil {
		t.Fatalf("ExecuteWithTranscriptMetadata: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("analyze media tool emitted no progress")
	}
	for _, emitted := range events {
		if emitted.Kind != event.VisionProgress || emitted.VisionProgress == nil {
			continue
		}
		if emitted.VisionProgress.OwnerKind != "tool" || emitted.VisionProgress.OwnerID != "call-vision" {
			t.Fatalf("visual progress owner = %+v", emitted.VisionProgress)
		}
	}
}

func TestAnalyzeMediaToolRevalidatesCustomDescriberEvidence(t *testing.T) {
	d := &analyzeToolDescriber{evidence: Evidence{Summary: "missing required collections"}}
	got := NewAnalyzeMediaTool("vision/model", d, func(context.Context, MediaSelection) ([]Image, []string, error) {
		return []Image{{DataURL: "data:image/png;base64,AA=="}}, nil, nil
	}, nil)
	executor := got.(toolpkg.TranscriptMetadataExecutor)
	result, err := executor.ExecuteWithTranscriptMetadata(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "ModLens") {
		t.Fatalf("error = %v, want strict ModLens validation", err)
	}
	if len(result.VisualAnalyses) != 1 || result.VisualAnalyses[0].Status != string(event.VisionStageFailed) {
		t.Fatalf("failed analysis metadata = %+v", result.VisualAnalyses)
	}
}

func anyStrings(value any) []string {
	items, _ := value.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}
