package agent

import (
	"context"

	"reasonix/internal/provider"
)

func (a *Agent) prepareToolRoundOutputs(ctx context.Context, calls []provider.ToolCall, batch batchExecution) ([]string, [][]string, [][]provider.VisualAnalysisRecord) {
	results, images := batch.results, batch.images
	visualAnalyses := make([][]provider.VisualAnalysisRecord, len(calls))
	for i := range batch.visualAnalyses {
		visualAnalyses[i] = cloneVisualAnalyses(batch.visualAnalyses[i])
	}
	if a.toolImages == nil {
		return results, images, visualAnalyses
	}
	results, images, bridged := a.processToolImages(ctx, calls, results, images)
	for i := range bridged {
		visualAnalyses[i] = append(visualAnalyses[i], cloneVisualAnalyses(bridged[i])...)
	}
	return results, images, visualAnalyses
}
