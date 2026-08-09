package agent

import (
	"context"
	"encoding/json"

	"reasonix/internal/evidence"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

type concreteToolResult struct {
	output         string
	images         []string
	visualAnalyses []provider.VisualAnalysisRecord
	execution      *tool.ShellExecution
	err            error
}

func executeConcreteTool(ctx context.Context, runTool tool.Tool, args json.RawMessage, verification bool) concreteToolResult {
	var out concreteToolResult
	if detailedExecutor, ok := runTool.(tool.DetailedExecutor); ok {
		detailed, err := detailedExecutor.ExecuteDetailed(ctx, args)
		out.output, out.images, out.execution, out.err = detailed.Output, detailed.Images, detailed.Execution, err
		if out.execution != nil && verification {
			if err != nil {
				out.execution.Verification = tool.ShellVerificationFailed
			} else {
				out.execution.Verification = tool.ShellVerificationPassed
			}
		} else if out.execution != nil && out.execution.Verification == "" {
			out.execution.Verification = tool.ShellVerificationNotVerification
		}
		if out.execution != nil && evidence.BashCommandMayBeOpaqueMutation(args) && out.execution.MutationRisk == tool.ShellMutationMayHaveCompleted {
			out.execution.MutationRisk = tool.ShellMutationUnknown
		}
		return out
	}
	if metadataExecutor, ok := runTool.(tool.TranscriptMetadataExecutor); ok {
		metadata, err := metadataExecutor.ExecuteWithTranscriptMetadata(ctx, args)
		out.output, out.images, out.err = metadata.Output, metadata.Images, err
		out.visualAnalyses = cloneVisualAnalyses(metadata.VisualAnalyses)
		return out
	}
	if imageTool, ok := runTool.(tool.ImageTool); ok {
		out.output, out.images, out.err = imageTool.ExecuteWithImages(ctx, args)
		return out
	}
	out.output, out.err = runTool.Execute(ctx, args)
	return out
}
