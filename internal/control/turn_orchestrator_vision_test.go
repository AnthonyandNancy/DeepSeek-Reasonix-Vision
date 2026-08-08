package control

import (
	"context"
	"testing"

	"reasonix/internal/event"
)

func TestTurnOrchestratorStartsVisibleTurnBeforeVisualEvidence(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeImageRouteConfig(t, dir)
	ref, err := SaveImageDataURL("data:image/png;base64," + tinyPNG)
	if err != nil {
		t.Fatalf("SaveImageDataURL: %v", err)
	}

	var events []event.Event
	runner := &fakeTurnRunner{}
	c := New(Options{
		Runner:          runner,
		ModelRef:        "text/main",
		WorkspaceRoot:   dir,
		VisionModelRef:  "vision/vl",
		VisionDescriber: &routeEvidenceDescriber{},
		Sink:            event.FuncSink(func(e event.Event) { events = append(events, e) }),
	})
	if err := newTurnOrchestrator(c).runTurnWithRawDisplay(context.Background(), "inspect @"+ref, "inspect @"+ref, "inspect"); err != nil {
		t.Fatalf("run turn: %v", err)
	}

	started := -1
	progress := -1
	for i, e := range events {
		if e.Kind == event.TurnStarted && started < 0 {
			started = i
		}
		if e.Kind == event.VisionProgress && progress < 0 {
			progress = i
		}
	}
	if started < 0 || progress < 0 || started > progress {
		t.Fatalf("event order = %+v, want TurnStarted before VisionProgress", events)
	}
}
