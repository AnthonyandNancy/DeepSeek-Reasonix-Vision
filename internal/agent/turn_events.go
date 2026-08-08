package agent

import (
	"context"

	"reasonix/internal/event"
)

type turnStartedEventContextKey struct{}

// WithTurnStartedEventEmitted marks a host-owned turn started before preparation.
func WithTurnStartedEventEmitted(ctx context.Context) context.Context {
	return context.WithValue(ctx, turnStartedEventContextKey{}, true)
}

func turnStartedEventEmitted(ctx context.Context) bool {
	emitted, _ := ctx.Value(turnStartedEventContextKey{}).(bool)
	return emitted
}

func (a *Agent) emitTurnStarted(ctx context.Context) {
	if !turnStartedEventEmitted(ctx) {
		a.sink.Emit(event.Event{Kind: event.TurnStarted})
	}
}
