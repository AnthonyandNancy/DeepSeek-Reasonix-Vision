package boot

import (
	"context"
	"errors"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/tool"
	"reasonix/internal/vision"
)

func wireVisionTools(reg *tool.Registry, modelRef string, describer vision.Describer, sink event.Sink, controller func() *control.Controller) *vision.ProviderToolImageProcessor {
	if describer != nil && modelRef != "" {
		reg.Add(vision.NewAnalyzeMediaTool(modelRef, describer, func(ctx context.Context, selection vision.MediaSelection) ([]vision.Image, []string, error) {
			ctrl := controller()
			if ctrl == nil {
				return nil, nil, errors.New("controller is not ready")
			}
			return ctrl.ResolveHistoricalVisionMedia(ctx, selection)
		}, func(ctx context.Context) string {
			parentID, _, _, ok := agent.CallContext(ctx)
			if !ok {
				return ""
			}
			return parentID
		}))
	}
	return vision.NewToolImageProcessor(modelRef, describer, sink)
}
