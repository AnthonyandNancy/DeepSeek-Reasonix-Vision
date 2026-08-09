package control

import (
	"context"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/tool"
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
	var firstProgress *event.VisionProgressInfo
	for i, e := range events {
		if e.Kind == event.TurnStarted && started < 0 {
			started = i
		}
		if e.Kind == event.VisionProgress && progress < 0 {
			progress = i
			firstProgress = e.VisionProgress
		}
	}
	if started < 0 || progress < 0 || started > progress {
		t.Fatalf("event order = %+v, want TurnStarted before VisionProgress", events)
	}
	if firstProgress == nil || firstProgress.OwnerKind != event.VisionOwnerUser || firstProgress.OwnerID != "" {
		t.Fatalf("host visual owner = %+v, want active user", firstProgress)
	}
}

func TestTurnOrchestratorPersistsHostVisualAnalysisOnUserTurn(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeImageRouteConfig(t, dir)
	ref, err := SaveImageDataURL("data:image/png;base64," + tinyPNG)
	if err != nil {
		t.Fatalf("SaveImageDataURL: %v", err)
	}

	sess := agent.NewSession("sys")
	exec := agent.New(
		testutil.NewMock("main", testutil.Turn{Text: "done"}),
		tool.NewRegistry(), sess, agent.Options{}, event.Discard,
	)
	c := New(Options{
		Runner: exec, Executor: exec, ModelRef: "text/main", WorkspaceRoot: dir,
		VisionModelRef: "vision/vl", VisionDescriber: &routeEvidenceDescriber{}, Sink: event.Discard,
	})
	if err := newTurnOrchestrator(c).runTurnWithRawDisplay(context.Background(), "inspect @"+ref, "inspect @"+ref, "inspect"); err != nil {
		t.Fatalf("run turn: %v", err)
	}

	msgs := sess.Snapshot()
	if len(msgs) < 2 || len(msgs[1].VisualAnalyses) != 1 {
		t.Fatalf("session lost host visual analysis: %+v", msgs)
	}
	if got := msgs[1].VisualAnalyses[0]; got.Initiator != "host_auto" || got.Status != "ready" || got.Summary != "dialog clipped" {
		t.Fatalf("persisted host visual analysis = %+v", got)
	}
}
