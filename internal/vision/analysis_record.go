package vision

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

const (
	AnalysisInitiatorHostAuto        = "host_auto"
	AnalysisInitiatorMainModelTool   = "main_model_tool"
	AnalysisInitiatorToolMediaBridge = "tool_media_bridge"
)

var analysisIDSequence atomic.Uint64

func NewAnalysisID() string {
	return fmt.Sprintf("vision-%d-%d", time.Now().UnixMilli(), analysisIDSequence.Add(1))
}

type progressScopeKey struct{}

type ProgressScope struct {
	AnalysisID string
	Initiator  string
	OwnerKind  event.VisionProgressOwnerKind
	OwnerID    string
	MediaCount int
	Observe    func(event.VisionProgressInfo)
}

type progressScopeState struct {
	mu               sync.Mutex
	config           ProgressScope
	attempt          int
	attemptStartedAt time.Time
	timing           progressTiming
}

func WithProgressScope(ctx context.Context, scope ProgressScope) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, progressScopeKey{}, &progressScopeState{config: scope, timing: newProgressTiming()})
}

func ensureProgressScope(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Value(progressScopeKey{}).(*progressScopeState); ok {
		return ctx
	}
	return WithProgressScope(ctx, ProgressScope{})
}

func emitProgressEvent(ctx context.Context, sink event.Sink, info event.VisionProgressInfo) {
	if ctx == nil {
		ctx = context.Background()
	}
	if scope, _ := ctx.Value(progressScopeKey{}).(*progressScopeState); scope != nil {
		scope.mu.Lock()
		previousAttempt := scope.attempt
		if info.Attempt > 0 {
			scope.attempt = info.Attempt
		} else if info.Stage == event.VisionStagePreparing {
			scope.attempt++
		} else if scope.attempt == 0 {
			scope.attempt = 1
		}
		now := time.Now()
		if scope.attemptStartedAt.IsZero() || scope.attempt != previousAttempt {
			scope.attemptStartedAt = now
		}
		info.AnalysisID = scope.config.AnalysisID
		info.Initiator = scope.config.Initiator
		info.OwnerKind = scope.config.OwnerKind
		info.OwnerID = scope.config.OwnerID
		info.MediaCount = scope.config.MediaCount
		info.Attempt = scope.attempt
		if info.ElapsedMs <= 0 {
			info.ElapsedMs = max(int64(0), now.Sub(scope.attemptStartedAt).Milliseconds())
		}
		timing := scope.timing.observe(info.Attempt, info.Stage, info.ElapsedMs)
		info.ElapsedMs = timing.totalElapsedMs
		info.StageElapsedMs = timing.stageElapsedMs
		info.CompletedStage = timing.completedStage
		info.CompletedStageAttempt = timing.completedStageAttempt
		info.CompletedStageElapsedMs = timing.completedStageElapsedMs
		observe := scope.config.Observe
		scope.mu.Unlock()
		if observe != nil {
			observe(info)
		}
	}
	if sink != nil {
		sink.Emit(event.Event{
			Kind: event.VisionProgress, ModelRef: info.ModelRef,
			Source: event.UsageSourceVision, UsageSource: event.UsageSourceVision,
			VisionProgress: &info,
		})
	}
}

// EmitProgress publishes one scoped visual-analysis lifecycle update.
func EmitProgress(ctx context.Context, sink event.Sink, info event.VisionProgressInfo) {
	emitProgressEvent(ctx, sink, info)
}

const (
	maxAnalysisStages         = 64
	maxAnalysisResponseBytes  = 12 << 10
	maxAnalysisReasoningBytes = 8 << 10
	maxAnalysisDetailBytes    = 1 << 10
	maxAnalysisSummaryBytes   = 4 << 10
	maxAnalysisOCRBytes       = 16 << 10
	maxAnalysisEvidenceBytes  = 48 << 10
	maxAnalysisMediaRefs      = 32
	maxAnalysisMediaRefBytes  = 1 << 10
)

type AnalysisRecorder struct {
	mu     sync.Mutex
	record provider.VisualAnalysisRecord
}

func NewAnalysisRecorder(id, initiator, modelRef string, mediaRefs []string, mediaCount int) *AnalysisRecorder {
	return &AnalysisRecorder{record: provider.VisualAnalysisRecord{
		ID: id, Initiator: initiator, ModelRef: modelRef,
		MediaRefs: boundedAnalysisRefs(mediaRefs), MediaCount: mediaCount,
		Status: "preparing", StartedAt: time.Now().UnixMilli(),
	}}
}

func (r *AnalysisRecorder) Observe(info event.VisionProgressInfo) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if info.CompletedStage != "" {
		r.updateStageDurationLocked(info.CompletedStageAttempt, info.CompletedStage, info.CompletedStageElapsedMs)
	}
	attempt := info.Attempt
	if attempt <= 0 {
		attempt = 1
	}
	stage := string(info.Stage)
	if stage == "" {
		return
	}
	index := -1
	for i := len(r.record.Stages) - 1; i >= 0; i-- {
		candidate := r.record.Stages[i]
		if candidate.Attempt == attempt && candidate.Stage == stage {
			index = i
			break
		}
	}
	if index < 0 {
		if len(r.record.Stages) >= maxAnalysisStages {
			index = len(r.record.Stages) - 1
		} else {
			r.record.Stages = append(r.record.Stages, provider.VisualAnalysisStage{Attempt: attempt, Stage: stage})
			index = len(r.record.Stages) - 1
		}
	}
	row := &r.record.Stages[index]
	row.Response = appendBoundedAnalysis(row.Response, info.ResponseDelta, maxAnalysisResponseBytes)
	row.Reasoning = appendBoundedAnalysis(row.Reasoning, info.ReasoningDelta, maxAnalysisReasoningBytes)
	if info.Detail != "" {
		row.Detail = truncateAnalysisText(info.Detail, maxAnalysisDetailBytes)
	}
	if info.StageElapsedMs > row.DurationMs {
		row.DurationMs = info.StageElapsedMs
	}
	if info.ElapsedMs > r.record.ElapsedMs {
		r.record.ElapsedMs = info.ElapsedMs
	}
	r.record.Status = stage
	if stage == string(event.VisionStageReady) || stage == string(event.VisionStageFailed) || stage == string(event.VisionStageCancelled) {
		r.record.CompletedAt = time.Now().UnixMilli()
	}
}

func (r *AnalysisRecorder) updateStageDurationLocked(attempt int, stage event.VisionProgressStage, duration int64) {
	if r == nil || stage == "" || duration < 0 {
		return
	}
	if attempt <= 0 {
		attempt = 1
	}
	for index := len(r.record.Stages) - 1; index >= 0; index-- {
		row := &r.record.Stages[index]
		if row.Attempt != attempt || row.Stage != string(stage) {
			continue
		}
		if duration > row.DurationMs {
			row.DurationMs = duration
		}
		return
	}
	if len(r.record.Stages) >= maxAnalysisStages {
		return
	}
	r.record.Stages = append(r.record.Stages, provider.VisualAnalysisStage{
		Attempt: attempt, Stage: string(stage), DurationMs: duration,
	})
}

func (r *AnalysisRecorder) Snapshot(evidence Evidence, renderedEvidence string) provider.VisualAnalysisRecord {
	if r == nil {
		return provider.VisualAnalysisRecord{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.record
	out.MediaRefs = append([]string(nil), r.record.MediaRefs...)
	out.Stages = append([]provider.VisualAnalysisStage(nil), r.record.Stages...)
	out.Summary = truncateAnalysisText(evidence.Summary, maxAnalysisSummaryBytes)
	out.OCRText = truncateAnalysisText(analysisOCRText(evidence.OCR), maxAnalysisOCRBytes)
	out.Evidence = truncateAnalysisText(renderedEvidence, maxAnalysisEvidenceBytes)
	if out.CompletedAt == 0 && (out.Status == string(event.VisionStageReady) || out.Status == string(event.VisionStageFailed) || out.Status == string(event.VisionStageCancelled)) {
		out.CompletedAt = time.Now().UnixMilli()
	}
	return out
}

func analysisOCRText(ocr OCR) string {
	if text := strings.TrimSpace(ocr.FullText); text != "" {
		return text
	}
	lines := make([]string, 0, len(ocr.Lines))
	for _, line := range ocr.Lines {
		if text := strings.TrimSpace(line.Text); text != "" {
			lines = append(lines, text)
		}
	}
	return strings.Join(lines, "\n")
}

func boundedAnalysisRefs(refs []string) []string {
	if len(refs) > maxAnalysisMediaRefs {
		refs = refs[:maxAnalysisMediaRefs]
	}
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref = strings.TrimSpace(ref); ref != "" {
			out = append(out, truncateAnalysisText(ref, maxAnalysisMediaRefBytes))
		}
	}
	return out
}

func appendBoundedAnalysis(current, delta string, maxBytes int) string {
	if delta == "" || maxBytes <= 0 {
		return truncateAnalysisText(current, maxBytes)
	}
	return truncateAnalysisText(current+delta, maxBytes)
}

func truncateAnalysisText(text string, maxBytes int) string {
	if maxBytes <= 0 || len(text) <= maxBytes {
		return text
	}
	text = text[:maxBytes]
	for len(text) > 0 && !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}
