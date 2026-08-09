package agent

import (
	"context"
	"strings"

	"reasonix/internal/provider"
	"reasonix/internal/vision"
)

const toolImageTaskContextLimit = 2 * 1024

func (a *Agent) processToolImages(ctx context.Context, calls []provider.ToolCall, results []string, images [][]string) ([]string, [][]string) {
	if a.toolImages == nil {
		return results, images
	}
	results = append([]string(nil), results...)
	images = append([][]string(nil), images...)
	taskContext := a.currentTaskContext()
	for i := range calls {
		if len(images[i]) == 0 {
			continue
		}
		out := a.toolImages.ProcessToolImages(ctx, vision.ToolImageInput{
			ToolName: calls[i].Name, ToolCallID: calls[i].ID, ToolText: results[i], Images: images[i],
			ModelRef: a.modelRef, ModelSupportsImages: a.modelSupportsImages,
			TaskContext: taskContext, MaxTextBytes: maxToolOutputBytes,
		})
		results[i], images[i] = out.Text, out.Images
	}
	return results, images
}

func (a *Agent) currentTaskContext() string {
	if content := strings.TrimSpace(a.classifierTaskText); content != "" {
		return boundToolImageTaskContext(content)
	}
	if a.session == nil || a.session.Len() == 0 {
		return ""
	}
	msgs := a.session.Snapshot()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != provider.RoleUser {
			continue
		}
		content := strings.TrimSpace(msgs[i].RawContent)
		if content == "" {
			content = strings.TrimSpace(msgs[i].Content)
		}
		if content != "" {
			return boundToolImageTaskContext(content)
		}
	}
	return ""
}

func boundToolImageTaskContext(content string) string {
	if len(content) <= toolImageTaskContextLimit {
		return content
	}
	cut := toolImageTaskContextLimit
	for cut > 0 && content[cut]&0xc0 == 0x80 {
		cut--
	}
	return content[:cut] + "…[truncated]…"
}

func retainedLocalToolImages(original, providerVisible []string) []string {
	if len(original) == 0 || len(providerVisible) > 0 {
		return nil
	}
	return original
}
