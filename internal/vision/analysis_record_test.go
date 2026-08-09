package vision

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"reasonix/internal/event"
)

func TestProgressScopeAssignsIdentityAndAttempts(t *testing.T) {
	var emitted []event.Event
	var observed []event.VisionProgressInfo
	ctx := WithProgressScope(context.Background(), ProgressScope{
		AnalysisID: "vision-live", Initiator: "host_auto", MediaCount: 2,
		Observe: func(info event.VisionProgressInfo) { observed = append(observed, info) },
	})
	sink := event.FuncSink(func(e event.Event) { emitted = append(emitted, e) })

	emitProgressEvent(ctx, sink, event.VisionProgressInfo{Stage: event.VisionStagePreparing})
	emitProgressEvent(ctx, sink, event.VisionProgressInfo{Stage: event.VisionStageResponse, ResponseDelta: "chunk"})
	emitProgressEvent(ctx, sink, event.VisionProgressInfo{Stage: event.VisionStageFailed})
	emitProgressEvent(ctx, sink, event.VisionProgressInfo{Stage: event.VisionStagePreparing})

	if len(emitted) != 4 || len(observed) != 4 {
		t.Fatalf("emitted=%d observed=%d, want 4", len(emitted), len(observed))
	}
	first := emitted[0].VisionProgress
	if first == nil || first.AnalysisID != "vision-live" || first.Initiator != "host_auto" || first.MediaCount != 2 || first.Attempt != 1 {
		t.Fatalf("first progress identity = %+v", first)
	}
	if observed[1].Attempt != 1 || observed[3].Attempt != 2 {
		t.Fatalf("attempt sequence = %+v", observed)
	}
}

func TestAnalysisRecorderMergesLifecycleIntoDurableRecord(t *testing.T) {
	r := NewAnalysisRecorder("vision-1", "host_auto", "provider/vision", []string{"@shot.png"}, 1)
	ctx := WithProgressScope(context.Background(), ProgressScope{
		AnalysisID: "vision-1", Initiator: "host_auto", MediaCount: 1, Observe: r.Observe,
	})
	EmitProgress(ctx, nil, event.VisionProgressInfo{Stage: event.VisionStagePreparing, ElapsedMs: 1})
	EmitProgress(ctx, nil, event.VisionProgressInfo{Stage: event.VisionStageResponse, ResponseDelta: "A", ElapsedMs: 2})
	EmitProgress(ctx, nil, event.VisionProgressInfo{Stage: event.VisionStageThinking, ReasoningDelta: "B", ElapsedMs: 3})
	EmitProgress(ctx, nil, event.VisionProgressInfo{Stage: event.VisionStageResponse, ResponseDelta: "C", ElapsedMs: 4})
	EmitProgress(ctx, nil, event.VisionProgressInfo{Stage: event.VisionStageReady, ElapsedMs: 5})

	evidence := Evidence{Summary: "settings dialog", OCR: OCR{FullText: "Save"}}
	got := r.Snapshot(evidence, `<visual-evidence schema="modlens-v2">settings</visual-evidence>`)
	if got.ID != "vision-1" || got.Initiator != "host_auto" || got.ModelRef != "provider/vision" || got.Status != "ready" {
		t.Fatalf("record identity/status = %+v", got)
	}
	if got.MediaCount != 1 || len(got.MediaRefs) != 1 || got.MediaRefs[0] != "@shot.png" {
		t.Fatalf("record media = %+v", got)
	}
	if got.Summary != "settings dialog" || got.OCRText != "Save" || !strings.Contains(got.Evidence, "modlens-v2") {
		t.Fatalf("record evidence = %+v", got)
	}
	if len(got.Stages) != 4 {
		t.Fatalf("stages = %+v, want preparing/response/thinking/ready", got.Stages)
	}
	if got.Stages[1].Stage != "response" || got.Stages[1].Response != "AC" {
		t.Fatalf("response stage did not merge deltas: %+v", got.Stages[1])
	}
	if got.Stages[2].Reasoning != "B" || got.ElapsedMs != 5 || got.StartedAt == 0 || got.CompletedAt == 0 {
		t.Fatalf("record timing/reasoning = %+v", got)
	}
}

func TestAnalysisRecorderBoundsScopedTranscriptData(t *testing.T) {
	refs := make([]string, maxAnalysisMediaRefs+5)
	for i := range refs {
		refs[i] = strings.Repeat("媒", maxAnalysisMediaRefBytes)
	}
	r := NewAnalysisRecorder("vision-bounded", "host_auto", "provider/vision", refs, len(refs))
	ctx := WithProgressScope(context.Background(), ProgressScope{
		AnalysisID: "vision-bounded", Initiator: "host_auto", MediaCount: len(refs), Observe: r.Observe,
	})
	EmitProgress(ctx, nil, event.VisionProgressInfo{Stage: event.VisionStagePreparing, Detail: strings.Repeat("d", maxAnalysisDetailBytes+100)})
	EmitProgress(ctx, nil, event.VisionProgressInfo{Stage: event.VisionStageResponse, ResponseDelta: strings.Repeat("r", maxAnalysisResponseBytes+100)})
	EmitProgress(ctx, nil, event.VisionProgressInfo{Stage: event.VisionStageThinking, ReasoningDelta: strings.Repeat("t", maxAnalysisReasoningBytes+100)})
	EmitProgress(ctx, nil, event.VisionProgressInfo{Stage: event.VisionStageReady})

	evidence := Evidence{
		Summary: strings.Repeat("s", maxAnalysisSummaryBytes+100),
		OCR:     OCR{FullText: strings.Repeat("o", maxAnalysisOCRBytes+100)},
	}
	got := r.Snapshot(evidence, strings.Repeat("e", maxAnalysisEvidenceBytes+100))
	if len(got.MediaRefs) != maxAnalysisMediaRefs {
		t.Fatalf("media refs = %d, want bounded to %d", len(got.MediaRefs), maxAnalysisMediaRefs)
	}
	for _, ref := range got.MediaRefs {
		if len(ref) > maxAnalysisMediaRefBytes || !utf8.ValidString(ref) {
			t.Fatalf("unbounded or invalid media ref: bytes=%d valid=%v", len(ref), utf8.ValidString(ref))
		}
	}
	if len(got.Summary) > maxAnalysisSummaryBytes || len(got.OCRText) > maxAnalysisOCRBytes || len(got.Evidence) > maxAnalysisEvidenceBytes {
		t.Fatalf("unbounded evidence fields: summary=%d ocr=%d evidence=%d", len(got.Summary), len(got.OCRText), len(got.Evidence))
	}
	for _, stage := range got.Stages {
		if len(stage.Response) > maxAnalysisResponseBytes || len(stage.Reasoning) > maxAnalysisReasoningBytes || len(stage.Detail) > maxAnalysisDetailBytes {
			t.Fatalf("unbounded stage: %+v", stage)
		}
	}
}

func TestAnalysisRecorderStartsNewAttemptAfterFailure(t *testing.T) {
	r := NewAnalysisRecorder("vision-2", "host_auto", "provider/vision", nil, 1)
	r.Observe(event.VisionProgressInfo{Stage: event.VisionStagePreparing, Attempt: 1})
	r.Observe(event.VisionProgressInfo{Stage: event.VisionStageFailed, Attempt: 1, Detail: "timeout"})
	r.Observe(event.VisionProgressInfo{Stage: event.VisionStagePreparing, Attempt: 2})
	r.Observe(event.VisionProgressInfo{Stage: event.VisionStageReady, Attempt: 2})

	got := r.Snapshot(Evidence{Summary: "retry ok"}, "evidence")
	if len(got.Stages) != 4 || got.Stages[0].Attempt != 1 || got.Stages[2].Attempt != 2 || got.Status != "ready" {
		t.Fatalf("retry lifecycle = %+v", got)
	}
}
