package vision

import "reasonix/internal/event"

type progressTimingKey struct {
	attempt int
	stage   event.VisionProgressStage
}

type progressTimingUpdate struct {
	totalElapsedMs          int64
	stageElapsedMs          int64
	completedStage          event.VisionProgressStage
	completedStageAttempt   int
	completedStageElapsedMs int64
}

type progressTiming struct {
	active          progressTimingKey
	activeStartMs   int64
	activeSet       bool
	lastTotalMs     int64
	lastRawTotalMs  int64
	lastAttempt     int
	attemptOffsetMs int64
	accumulatedMs   map[progressTimingKey]int64
}

func newProgressTiming() progressTiming {
	return progressTiming{accumulatedMs: make(map[progressTimingKey]int64)}
}

func (t *progressTiming) observe(attempt int, stage event.VisionProgressStage, totalMs int64) progressTimingUpdate {
	if t == nil || stage == "" {
		return progressTimingUpdate{}
	}
	if attempt <= 0 {
		attempt = 1
	}
	if totalMs < 0 {
		totalMs = 0
	}
	rawTotalMs := totalMs
	if t.activeSet && attempt != t.lastAttempt {
		if rawTotalMs < t.lastRawTotalMs {
			t.attemptOffsetMs = t.lastTotalMs
		} else {
			t.attemptOffsetMs = 0
		}
	}
	totalMs = rawTotalMs + t.attemptOffsetMs
	if totalMs < t.lastTotalMs {
		totalMs = t.lastTotalMs
	}
	t.lastRawTotalMs = rawTotalMs
	t.lastAttempt = attempt
	t.lastTotalMs = totalMs
	key := progressTimingKey{attempt: attempt, stage: stage}
	update := progressTimingUpdate{totalElapsedMs: totalMs}
	if !t.activeSet {
		t.active = key
		t.activeStartMs = totalMs
		t.activeSet = true
	} else if t.active != key {
		completed := t.durationFor(t.active, totalMs)
		t.accumulatedMs[t.active] = completed
		update.completedStage = t.active.stage
		update.completedStageAttempt = t.active.attempt
		update.completedStageElapsedMs = completed
		t.active = key
		t.activeStartMs = totalMs
	}
	update.stageElapsedMs = t.durationFor(key, totalMs)
	return update
}

func (t *progressTiming) durationFor(key progressTimingKey, totalMs int64) int64 {
	if t == nil {
		return 0
	}
	duration := t.accumulatedMs[key]
	if t.activeSet && t.active == key && totalMs > t.activeStartMs {
		duration += totalMs - t.activeStartMs
	}
	return duration
}
