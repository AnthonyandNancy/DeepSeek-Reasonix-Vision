package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

type userInputCaptureProvider struct {
	request  provider.Request
	requests []provider.Request
}

func (p *userInputCaptureProvider) Name() string { return "capture" }

func (p *userInputCaptureProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.request = req
	p.requests = append(p.requests, req)
	ch := make(chan provider.Chunk, 1)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: "done"}
	close(ch)
	return ch, nil
}

func TestProviderHistoryDropsOldCapabilityRouteButKeepsCurrentTurnRoute(t *testing.T) {
	prov := &userInputCaptureProvider{}
	sess := NewSession("system")
	a := New(prov, tool.NewRegistry(), sess, Options{}, event.Discard)
	first := `<capability-route version="1">
old MCP route
</capability-route>

<visual-model-assistance version="1">
old visual host guidance
</visual-model-assistance>

<visual-evidence schema="modlens-v2">
old evidence
</visual-evidence>

first request`
	second := `<capability-route version="1">
current route
</capability-route>

<visual-model-assistance version="1">
current visual host guidance
</visual-model-assistance>

second request`
	if err := a.Run(context.Background(), first); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if err := a.Run(context.Background(), second); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if len(prov.requests) != 2 {
		t.Fatalf("provider requests = %d, want two", len(prov.requests))
	}
	var old, current string
	for _, message := range prov.requests[1].Messages {
		if message.Role != provider.RoleUser {
			continue
		}
		if strings.Contains(message.Content, "first request") {
			old = message.Content
		}
		if strings.Contains(message.Content, "second request") {
			current = message.Content
		}
	}
	if strings.Contains(old, "<capability-route") || strings.Contains(old, "<visual-model-assistance") {
		t.Fatalf("old transient route leaked into second request: %q", old)
	}
	if !strings.Contains(old, `<visual-evidence schema="modlens-v2">`) {
		t.Fatalf("durable ModLens evidence was removed from old history: %q", old)
	}
	if !strings.Contains(current, "<capability-route") || !strings.Contains(current, "<visual-model-assistance") {
		t.Fatalf("current-turn controls were removed: %q", current)
	}
}

func TestRunPersistsRawUserInputSeparatelyFromProviderContext(t *testing.T) {
	prov := &userInputCaptureProvider{}
	sess := NewSession("system")
	a := New(prov, tool.NewRegistry(), sess, Options{}, event.Discard)

	const raw = "fix the bug"
	const composed = "<capability-route version=\"1\">\nuse review\n</capability-route>\n\nfix the bug"
	ctx := WithRawUserInput(context.Background(), raw)
	if err := a.Run(ctx, composed); err != nil {
		t.Fatalf("Run: %v", err)
	}

	stored := sess.Snapshot()
	if len(stored) < 2 {
		t.Fatalf("stored messages = %d, want system and user", len(stored))
	}
	if got := stored[1].Content; got != composed {
		t.Fatalf("stored provider content = %q, want composed %q", got, composed)
	}
	if got := stored[1].RawContent; got != raw {
		t.Fatalf("stored raw content = %q, want raw %q", got, raw)
	}
	if stored[1].ProviderContent != "" {
		t.Fatalf("stored transitional provider content was not cleared: %+v", stored[1])
	}
	if len(prov.request.Messages) < 2 || prov.request.Messages[1].Content != composed {
		t.Fatalf("provider request did not receive composed context: %+v", prov.request.Messages)
	}
	if prov.request.Messages[1].RawContent != "" || prov.request.Messages[1].ProviderContent != "" {
		t.Fatalf("provider request leaked display metadata: %+v", prov.request.Messages[1])
	}

	encoded, err := json.Marshal(stored[1])
	if err != nil {
		t.Fatalf("marshal stored user turn: %v", err)
	}
	var legacy struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(encoded, &legacy); err != nil {
		t.Fatalf("decode with previous-release shape: %v", err)
	}
	if legacy.Content != composed {
		t.Fatalf("previous-release reader sees %q, want provider-visible %q", legacy.Content, composed)
	}
}

func TestRunPersistsStructuredMediaRefsButStripsThemFromProviderRequest(t *testing.T) {
	prov := &userInputCaptureProvider{}
	sess := NewSession("system")
	a := New(prov, tool.NewRegistry(), sess, Options{}, event.Discard)
	ctx := WithUserMediaRefs(context.Background(), []string{"@.reasonix/attachments/shot.png"})
	if err := a.Run(ctx, "look at the image"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	stored := sess.Snapshot()
	if len(stored) < 2 || len(stored[1].MediaRefs) != 1 || stored[1].MediaRefs[0] != "@.reasonix/attachments/shot.png" {
		t.Fatalf("stored media refs = %+v, want one structured attachment ref", stored)
	}
	if len(prov.request.Messages) < 2 || len(prov.request.Messages[1].MediaRefs) != 0 {
		t.Fatalf("provider request leaked media refs: %+v", prov.request.Messages)
	}
}
