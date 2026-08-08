package anthropic

import (
	"context"
	"testing"

	"reasonix/internal/provider"
)

func TestMessagesRequestSerializesJSONResponseFormat(t *testing.T) {
	p, err := New(provider.Config{Name: "anthropic", BaseURL: "https://api.anthropic.com", Model: "claude-vision"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	body := p.(*client).buildRequest(context.Background(), provider.Request{
		Messages:       []provider.Message{{Role: provider.RoleUser, Content: "describe"}},
		ResponseFormat: &provider.ResponseFormat{Type: "json_object"},
	})
	if body.OutputConfig == nil || body.OutputConfig.Format == nil || body.OutputConfig.Format.Type != "json_schema" {
		t.Fatalf("output_config = %+v, want official JSON schema format", body.OutputConfig)
	}
	if string(body.OutputConfig.Format.Schema) != `{"type":"object"}` {
		t.Fatalf("schema = %s", body.OutputConfig.Format.Schema)
	}
}
