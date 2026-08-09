package agent

import (
	"context"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

func TestBeginRunTurnPersistsUserVisualAnalyses(t *testing.T) {
	record := provider.VisualAnalysisRecord{
		ID: "vision-user", Initiator: "host_auto", Status: "ready", Summary: "dialog",
	}
	ctx := WithUserVisualAnalyses(context.Background(), []provider.VisualAnalysisRecord{record})
	a := &Agent{session: NewSession("sys"), sink: event.Discard}
	a.beginRunTurn(ctx, "inspect image")

	msgs := a.session.Snapshot()
	if len(msgs) != 2 || len(msgs[1].VisualAnalyses) != 1 {
		t.Fatalf("user turn lost visual analyses: %+v", msgs)
	}
	if got := msgs[1].VisualAnalyses[0]; got.ID != record.ID || got.Summary != record.Summary {
		t.Fatalf("persisted visual analysis = %+v, want %+v", got, record)
	}
}
