package provider

// VisualAnalysisStage is one bounded lifecycle row from an independent visual
// model request. It is local transcript metadata and never enters provider input.
type VisualAnalysisStage struct {
	Attempt   int    `json:"attempt,omitempty"`
	Stage     string `json:"stage"`
	Response  string `json:"response,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
	Detail    string `json:"detail,omitempty"`
	ElapsedMs int64  `json:"elapsed_ms,omitempty"`
}

// VisualAnalysisRecord is the durable local transcript representation of one
// independent visual-model operation and its final ModLens v2 evidence.
type VisualAnalysisRecord struct {
	ID          string                `json:"id"`
	Initiator   string                `json:"initiator"`
	ModelRef    string                `json:"model_ref,omitempty"`
	Status      string                `json:"status"`
	MediaRefs   []string              `json:"media_refs,omitempty"`
	MediaCount  int                   `json:"media_count,omitempty"`
	Stages      []VisualAnalysisStage `json:"stages,omitempty"`
	Summary     string                `json:"summary,omitempty"`
	OCRText     string                `json:"ocr_text,omitempty"`
	Evidence    string                `json:"evidence,omitempty"`
	StartedAt   int64                 `json:"started_at,omitempty"`
	CompletedAt int64                 `json:"completed_at,omitempty"`
	ElapsedMs   int64                 `json:"elapsed_ms,omitempty"`
}
