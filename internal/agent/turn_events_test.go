package agent

import (
	"context"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
)

func TestRunSkipsDuplicateTurnStartedWhenHostEmittedItBeforePreparation(t *testing.T) {
	provider := testutil.NewMock("m", testutil.Turn{Text: "done"})
	sink := &recordSink{}
	a := New(provider, echoRegistry(), NewSession("sys"), Options{}, sink)

	if err := a.Run(WithTurnStartedEventEmitted(context.Background()), "inspect images"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(sink.kinds(event.TurnStarted)); got != 0 {
		t.Fatalf("TurnStarted events = %d, want no duplicate host event", got)
	}
}
