package openai

import (
	"encoding/json"
	"testing"

	"reasonix/internal/provider"
)

func TestChatRequestSerializesJSONResponseFormat(t *testing.T) {
	client := &client{model: "vision-model"}
	body, err := json.Marshal(client.buildRequest(provider.Request{
		Messages:       []provider.Message{{Role: provider.RoleUser, Content: "describe"}},
		ResponseFormat: &provider.ResponseFormat{Type: "json_object"},
	}))
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	format, ok := wire["response_format"].(map[string]any)
	if !ok || format["type"] != "json_object" {
		t.Fatalf("response_format = %#v, want json_object", wire["response_format"])
	}
}
