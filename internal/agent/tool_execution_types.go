package agent

import (
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// batchExecution keeps each tool call's model output and provider-excluded
// transcript metadata aligned by call index.
type batchExecution struct {
	results            []string
	images             [][]string
	visualAnalyses     [][]provider.VisualAnalysisRecord
	executions         []*tool.ShellExecution
	recoveryStopTurn   bool
	recoveryStopReason string
}

// toolOutcome separates model-visible output from host-only execution and
// transcript metadata collected during one tool call.
type toolOutcome struct {
	output             string
	images             []string
	visualAnalyses     []provider.VisualAnalysisRecord
	blocked            bool
	errMsg             string
	truncated          bool
	truncMsg           string
	resolved           bool
	resolvedName       string
	capabilityID       string
	resolvedReadOnly   bool
	execution          *tool.ShellExecution
	recoveryGeneration uint64
	recoveryStopTurn   bool
	recoveryStopReason string
}
