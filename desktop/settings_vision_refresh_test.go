package main

import (
	"context"
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
)

func TestSetVisionModelRefreshesEveryTabRuntime(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OLD_VISION_KEY", "sk-old")
	setDesktopTestCredential(t, "NEW_VISION_KEY", "sk-new")

	cfg := config.Default()
	cfg.DefaultModel = "old/main"
	cfg.Agent.VisionModel = "old/vision-old"
	cfg.Desktop.ProviderAccess = []string{"old", "new"}
	cfg.Providers = []config.ProviderEntry{
		{Name: "old", Kind: "openai", BaseURL: "https://old.example.invalid/v1", APIKeyEnv: "OLD_VISION_KEY", Models: []string{"main", "vision-old"}, VisionModels: []string{"vision-old"}},
		{Name: "new", Kind: "openai", BaseURL: "https://new.example.invalid/v1", APIKeyEnv: "NEW_VISION_KEY", Models: []string{"main", "vision-new"}, VisionModels: []string{"vision-new"}},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	oldActive := control.New(control.Options{Label: "old/main", ModelRef: "old/main", VisionModelRef: "old/vision-old", Sink: event.Discard})
	oldSibling := control.New(control.Options{Label: "old/main", ModelRef: "old/main", VisionModelRef: "old/vision-old", Sink: event.Discard})
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	active := &WorkspaceTab{ID: "active", Scope: "global", Ready: true, model: "old/main", Ctrl: oldActive, disabledMCP: map[string]ServerView{}}
	sibling := &WorkspaceTab{ID: "sibling", Scope: "global", Ready: true, model: "old/main", Ctrl: oldSibling, disabledMCP: map[string]ServerView{}}
	app.tabs = map[string]*WorkspaceTab{active.ID: active, sibling.ID: sibling}
	app.tabOrder = []string{active.ID, sibling.ID}
	app.activeTabID = active.ID
	t.Cleanup(func() {
		for _, tab := range []*WorkspaceTab{active, sibling} {
			if tab.Ctrl != nil {
				tab.Ctrl.Close()
			}
			tab.releaseSessionLease()
		}
		oldActive.Close()
		oldSibling.Close()
	})

	if err := app.SetVisionModel("new/vision-new"); err != nil {
		t.Fatalf("SetVisionModel: %v", err)
	}
	activeCtrl, ok := active.Ctrl.(*control.Controller)
	if !ok {
		t.Fatalf("active controller = %T, want *control.Controller", active.Ctrl)
	}
	siblingCtrl, ok := sibling.Ctrl.(*control.Controller)
	if !ok {
		t.Fatalf("sibling controller = %T, want *control.Controller", sibling.Ctrl)
	}
	if got := activeCtrl.VisionModelRef(); got != "new/vision-new" {
		t.Fatalf("active vision model = %q, want new/vision-new", got)
	}
	if got := siblingCtrl.VisionModelRef(); got != "new/vision-new" {
		t.Fatalf("sibling vision model = %q, want new/vision-new", got)
	}
}

func TestSetVisionModelDefersOnlyLeaseBlockedSiblingTab(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OLD_VISION_KEY", "sk-old")
	setDesktopTestCredential(t, "NEW_VISION_KEY", "sk-new")

	cfg := config.Default()
	cfg.DefaultModel = "old/main"
	cfg.Agent.VisionModel = "old/vision-old"
	cfg.Desktop.ProviderAccess = []string{"old", "new"}
	cfg.Providers = []config.ProviderEntry{
		{Name: "old", Kind: "openai", BaseURL: "https://old.example.invalid/v1", APIKeyEnv: "OLD_VISION_KEY", Models: []string{"main", "vision-old"}, VisionModels: []string{"vision-old"}},
		{Name: "new", Kind: "openai", BaseURL: "https://new.example.invalid/v1", APIKeyEnv: "NEW_VISION_KEY", Models: []string{"main", "vision-new"}, VisionModels: []string{"vision-new"}},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	siblingPath := filepath.Join(t.TempDir(), "sibling.jsonl")
	externalLease, err := agent.TryAcquireSessionLease(siblingPath)
	if err != nil {
		t.Fatalf("acquire sibling lease: %v", err)
	}
	defer externalLease.Release()

	activeCtrl := control.New(control.Options{Label: "old/main", ModelRef: "old/main", VisionModelRef: "old/vision-old", Sink: event.Discard})
	siblingCtrl := control.New(control.Options{Label: "old/main", ModelRef: "old/main", VisionModelRef: "old/vision-old", SessionDir: filepath.Dir(siblingPath), SessionPath: siblingPath, Sink: event.Discard})
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	active := &WorkspaceTab{ID: "active", Scope: "global", Ready: true, model: "old/main", Ctrl: activeCtrl, disabledMCP: map[string]ServerView{}}
	sibling := &WorkspaceTab{ID: "sibling", Scope: "global", Ready: true, model: "old/main", SessionPath: siblingPath, Ctrl: siblingCtrl, disabledMCP: map[string]ServerView{}}
	app.tabs = map[string]*WorkspaceTab{active.ID: active, sibling.ID: sibling}
	app.tabOrder = []string{active.ID, sibling.ID}
	app.activeTabID = active.ID
	t.Cleanup(func() {
		for _, tab := range []*WorkspaceTab{active, sibling} {
			if tab.Ctrl != nil {
				tab.Ctrl.Close()
			}
			tab.releaseSessionLease()
		}
		activeCtrl.Close()
		siblingCtrl.Close()
	})

	if err := app.SetVisionModel("new/vision-new"); err != nil {
		t.Fatalf("SetVisionModel: %v", err)
	}
	if app.deferredRebuildPending(active.ID) {
		t.Fatal("active tab was incorrectly queued for sibling lease failure")
	}
	if !app.deferredRebuildPending(sibling.ID) {
		t.Fatal("lease-blocked sibling tab was not queued for deferred refresh")
	}
}
