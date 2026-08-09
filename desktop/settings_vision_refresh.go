package main

import (
	"errors"
	"fmt"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

// SetVisionModel sets or clears the model used to extract ModLens v2 evidence.
func (a *App) SetVisionModel(ref string) error {
	_, err := a.applyConfigChangeWithWarning("vision model", func(c *config.Config) error {
		ref = strings.TrimSpace(ref)
		if ref != "" {
			resolved, err := selectableDesktopModelRef(c, ref)
			if err != nil {
				return err
			}
			ref = resolved
		}
		c.Agent.VisionModel = ref
		return nil
	})
	return err
}

// rebuildSettingForAllTabs refreshes every live session because vision_model
// is user-global while each tab owns its own provider and describer instance.
func (a *App) rebuildSettingForAllTabs(setting string) error {
	if a.ctx == nil {
		return nil
	}
	a.runtimeRebuildMu.Lock()
	defer a.runtimeRebuildMu.Unlock()

	a.mu.RLock()
	tabs := make([]*WorkspaceTab, 0, len(a.tabOrder))
	seen := make(map[string]struct{}, len(a.tabOrder))
	for _, id := range a.tabOrder {
		tab := a.tabs[id]
		if tab == nil || tab.Ctrl == nil {
			continue
		}
		tabs = append(tabs, tab)
		seen[id] = struct{}{}
	}
	for id, tab := range a.tabs {
		if tab == nil || tab.Ctrl == nil {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		tabs = append(tabs, tab)
	}
	a.mu.RUnlock()

	var rebuildErrors []error
	for _, tab := range tabs {
		tab.turnStartMu.Lock()
		err := a.rebuildSettingTurnLocked(setting, tab, false, false)
		tab.turnStartMu.Unlock()
		if err == nil {
			continue
		}
		var busy *rebuildBusyError
		if errors.Is(err, agent.ErrSessionLeaseHeld) || errors.As(err, &busy) {
			a.scheduleDeferredRebuild(tab.ID, setting)
			userErr := err
			if errors.Is(err, agent.ErrSessionLeaseHeld) {
				userErr = userFacingSessionLeaseError(setting, err)
			}
			a.warnForTab(tab.ID, fmt.Sprintf("%s saved, but this session could not refresh yet: %s", setting, userErr.Error()))
			continue
		}
		rebuildErrors = append(rebuildErrors, fmt.Errorf("refresh tab %q: %w", tab.ID, err))
	}
	return errors.Join(rebuildErrors...)
}
