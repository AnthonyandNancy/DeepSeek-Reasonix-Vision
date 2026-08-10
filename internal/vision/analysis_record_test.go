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
		AnalysisID: "vision-live", Initiator: "tool_media_bridge", OwnerKind: "tool", OwnerID: "capture-1", MediaCount: 2,
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
	if first == nil || first.AnalysisID != "vision-live" || first.Initiator != "tool_media_bridge" || first.OwnerKind != "tool" || first.OwnerID != "capture-1" || first.MediaCount != 2 || first.Attempt != 1 {
		t.Fatalf("first progress identity = %+v", first)
	}
	if observed[1].Attempt != 1 || observed[3].Attempt != 2 {
		t.Fatalf("attempt sequence = %+v", observed)
	}
}

func TestProgressScopeTracksOwnStageDurationsSeparatelyFromTotal(t *testing.T) {
	var observed []event.VisionProgressInfo
	r := NewAnalysisRecorder("vision-timing", "host_auto", "provider/vision", nil, 1)
	ctx := WithProgressScope(context.Background(), ProgressScope{
		AnalysisID: "vision-timing", Initiator: "host_auto", Observe: func(info event.VisionProgressInfo) {
			observed = append(observed, info)
			r.Observe(info)
		},
	})

	EmitProgress(ctx, nil, event.VisionProgressInfo{Stage: event.VisionStagePreparing, Attempt: 1, ElapsedMs: 0})
	EmitProgress(ctx, nil, event.VisionProgressInfo{Stage: event.VisionStageConnecting, Attempt: 1, ElapsedMs: 150})
	EmitProgress(ctx, nil, event.VisionProgressInfo{Stage: event.VisionStageWaiting, Attempt: 1, ElapsedMs: 2_150})
	EmitProgress(ctx, nil, event.VisionProgressInfo{Stage: event.VisionStageResponse, Attempt: 1, ElapsedMs: 3_150})
	EmitProgress(ctx, nil, event.VisionProgressInfo{Stage: event.VisionStageResponse, Attempt: 1, ElapsedMs: 5_150})
	EmitProgress(ctx, nil, event.VisionProgressInfo{Stage: event.VisionStageThinking, Attempt: 1, ElapsedMs: 6_150})
	EmitProgress(ctx, nil, event.VisionProgressInfo{Stage: event.VisionStageResponse, Attempt: 1, ElapsedMs: 7_150})
	EmitProgress(ctx, nil, event.VisionProgressInfo{Stage: event.VisionStageParsing, Attempt: 1, ElapsedMs: 8_150})
	EmitProgress(ctx, nil, event.VisionProgressInfo{Stage: event.VisionStageReady, Attempt: 1, ElapsedMs: 8_200})

	if len(observed) != 9 {
		t.Fatalf("observed progress = %d, want 9", len(observed))
	}
	if got := observed[1]; got.CompletedStage != event.VisionStagePreparing || got.CompletedStageElapsedMs != 150 || got.StageElapsedMs != 0 {
		t.Fatalf("connecting timing = %+v", got)
	}
	if got := observed[5]; got.CompletedStage != event.VisionStageResponse || got.CompletedStageElapsedMs != 3_000 || got.StageElapsedMs != 0 {
		t.Fatalf("thinking timing = %+v", got)
	}
	if got := observed[7]; got.CompletedStage != event.VisionStageResponse || got.CompletedStageElapsedMs != 4_000 || got.StageElapsedMs != 0 {
		t.Fatalf("parsing timing = %+v", got)
	}

	got := r.Snapshot(Evidence{}, "")
	if got.ElapsedMs != 8_200 {
		t.Fatalf("total elapsed = %d, want 8200", got.ElapsedMs)
	}
	want := []int64{150, 2_000, 1_000, 4_000, 1_000, 50, 0}
	if len(got.Stages) != len(want) {
		t.Fatalf("stages = %+v, want %d rows", got.Stages, len(want))
	}
	for i, duration := range want {
		if got.Stages[i].DurationMs != duration {
			t.Fatalf("stage %d (%s) duration = %d, want %d", i, got.Stages[i].Stage, got.Stages[i].DurationMs, duration)
		}
	}
}

func TestProgressScopeKeepsRetryTimingMonotonicAcrossAttemptClocks(t *testing.T) {
	var observed []event.VisionProgressInfo
	ctx := WithProgressScope(context.Background(), ProgressScope{
		AnalysisID: "vision-retry-timing", Observe: func(info event.VisionProgressInfo) {
			observed = append(observed, info)
		},
	})
	EmitProgress(ctx, nil, event.VisionProgressInfo{Stage: event.VisionStagePreparing, Attempt: 1, ElapsedMs: 0})
	EmitProgress(ctx, nil, event.VisionProgressInfo{Stage: event.VisionStageResponse, Attempt: 1, ElapsedMs: 1_000})
	EmitProgress(ctx, nil, event.VisionProgressInfo{Stage: event.VisionStageFailed, Attempt: 1, ElapsedMs: 2_000})
	EmitProgress(ctx, nil, event.VisionProgressInfo{Stage: event.VisionStagePreparing, Attempt: 2, ElapsedMs: 0})
	EmitProgress(ctx, nil, event.VisionProgressInfo{Stage: event.VisionStageResponse, Attempt: 2, ElapsedMs: 500})
	EmitProgress(ctx, nil, event.VisionProgressInfo{Stage: event.VisionStageReady, Attempt: 2, ElapsedMs: 700})

	if len(observed) != 6 {
		t.Fatalf("observed progress = %d, want 6", len(observed))
	}
	if observed[3].ElapsedMs != 2_000 || observed[4].ElapsedMs != 2_500 || observed[5].ElapsedMs != 2_700 {
		t.Fatalf("retry total elapsed = %+v", observed)
	}
	if observed[4].StageElapsedMs != 0 || observed[5].CompletedStageElapsedMs != 200 {
		t.Fatalf("retry stage timing = %+v", observed)
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
