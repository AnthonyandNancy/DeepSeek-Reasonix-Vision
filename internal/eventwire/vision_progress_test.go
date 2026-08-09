package eventwire

import (
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/event"
)

func TestToWireVisionProgressCarriesBoundedLifecycleFields(t *testing.T) {
	w := ToWire(event.Event{
		Kind: event.VisionProgress,
		VisionProgress: &event.VisionProgressInfo{
			Stage: event.VisionStageResponse, AnalysisID: "vision-1", Initiator: "main_model_tool",
			OwnerKind: "tool", OwnerID: "call-vision", Attempt: 2, MediaCount: 3,
			ModelRef: "vision/model", ResponseDelta: "partial JSON", ElapsedMs: 125,
		},
	})
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"kind":"vision_progress"`, `"stage":"response"`, `"analysisId":"vision-1"`, `"initiator":"main_model_tool"`, `"ownerKind":"tool"`, `"ownerId":"call-vision"`, `"attempt":2`, `"mediaCount":3`, `"modelRef":"vision/model"`, `"responseDelta":"partial JSON"`, `"elapsedMs":125`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("vision progress JSON = %s, missing %s", b, want)
		}
	}
	if strings.Contains(string(b), "api_key") || strings.Contains(string(b), "data:image") || strings.Contains(string(b), "requestBody") {
		t.Fatalf("vision progress leaked sensitive/provider payload: %s", b)
	}
}
