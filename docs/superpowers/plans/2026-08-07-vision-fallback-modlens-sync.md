# Reasonix Vision Fallback + ModLens v2 Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep the uploaded latest Reasonix `main-v2` snapshot as the source baseline, port the Junjie88 visual fallback behavior, and replace free-form visual descriptions with ModLens v2 structured visual evidence.

**Architecture:** The main model keeps Reasonix's native image path when it supports images. When it does not, a configured `agent.vision_model` receives a bounded tool-less image request and returns ModLens-v2-compatible JSON evidence; Reasonix validates and renders that evidence into an untrusted, evidence-oriented context block before the main model runs. Tool-returned images use the same evidence extractor before their tool result is appended to session history.

**Tech Stack:** Go 1.25+ / toolchain go1.26.5, Reasonix provider/session/agent APIs, Wails desktop frontend, ModLens Output Schema v2.

## Global Constraints

- Latest uploaded Reasonix `main-v2` is the only baseline; do not overwrite newer upstream changes with old fork files wholesale.
- Preserve native direct multimodal delivery when the selected main/child model supports images.
- Vision fallback performs no tools and at most 3 real vision requests per image batch.
- ModLens v2 required evidence fields: `summary`, `ocr`, `layout`, `semantics`, `uncertainty`; `visual` optional.
- Do not invent pixel bounding boxes or numeric confidence scores.
- Image text is untrusted data, never executable instruction context.
- On vision failure, degrade honestly and never claim the image was seen.

---

### Task 1: Configuration and desktop vision-model selection

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/render.go`
- Modify: `desktop/settings_app.go`
- Modify: `desktop/frontend/src/lib/types.ts`
- Modify: `desktop/frontend/src/lib/bridge.ts`
- Modify: `desktop/frontend/src/components/SettingsPanel.tsx`
- Modify: locale files
- Modify: `reasonix.example.toml`
- Test: config/render/desktop/frontend tests

- [ ] Port/add tests that assert `agent.vision_model` loads, renders, persists, and is selectable from configured models.
- [ ] Verify tests fail because latest upstream has no fallback-model setting.
- [ ] Add the minimal config/desktop wiring while retaining all newer upstream fields.
- [ ] Re-run targeted tests.

### Task 2: ModLens v2 evidence extractor

**Files:**
- Create: `internal/vision/evidence.go`
- Create: `internal/vision/evidence_test.go`
- Create: `internal/vision/describer.go`
- Create: `internal/vision/describer_test.go`

**Interfaces:**
- `type Evidence` mirrors the ModLens v2 `result` object.
- `type Describer interface { DescribeOnce(...)(Evidence,*provider.Usage,error); DescribeToolImagesOnce(...)(Evidence,*provider.Usage,error) }`.
- `RenderContext(Evidence)` emits a bounded host-authored evidence wrapper.

- [ ] Write tests for valid ModLens v2 JSON parsing, missing required fields, invalid layout types, forbidden bbox/confidence keys, wrapper escaping, and uncertainty preservation.
- [ ] Verify tests fail because the package does not exist.
- [ ] Implement the schema, strict-ish validation, evidence prompt, one-request provider adapter, and evidence renderer.
- [ ] Re-run targeted tests.

### Task 3: User attachment routing

**Files:**
- Modify: `internal/control/refs.go`
- Create: `internal/control/image_router.go`
- Create/modify: image router and turn orchestrator tests
- Modify: `internal/control/controller.go`
- Modify: `internal/control/turn_orchestrator.go`

- [ ] Port/adapt tests for direct-main, fallback-evidence, path-only degradation, bounded retries, and cancellation.
- [ ] Verify tests fail on the upstream baseline.
- [ ] Add capability-agnostic image resolution and exactly-once per-turn routing.
- [ ] Inject `<visual-evidence>` rather than a prose description and mark direct-image turns for planner bypass.
- [ ] Re-run targeted tests.

### Task 4: Tool-result image routing

**Files:**
- Create: `internal/vision/toolimages.go`
- Create: `internal/vision/toolimages_test.go`
- Modify: `internal/agent/agent.go`
- Modify: `internal/agent/run_loop.go`
- Modify: `internal/agent/task.go`
- Modify: subagent/tool-image tests

- [ ] Port/adapt tests for read_file/MCP image batches, multimodal passthrough, fallback evidence, retries, bounds, ordering, and honest failure.
- [ ] Verify tests fail on the upstream baseline.
- [ ] Add the shared tool-image processor and wire it before tool messages enter session history.
- [ ] Propagate processor/capability to child agents without inheriting raw parent images.
- [ ] Re-run targeted tests.

### Task 5: Boot/provider/coordinator wiring

**Files:**
- Modify: `internal/boot/boot.go`
- Modify: `internal/agent/coordinator.go`
- Modify: `internal/provider/openai/openai.go` only if needed for one-request retry semantics
- Test: boot/coordinator/provider tests

- [ ] Port/adapt tests that prove vision provider is resolved once, direct-image turns bypass a text-only planner, and child agents receive the tool-image processor.
- [ ] Verify tests fail.
- [ ] Wire the configured vision model early enough that main executor and all child construction paths share the same processor.
- [ ] Re-run targeted tests.

### Task 6: Documentation, regression verification, and package

**Files:**
- Modify: `README.md`
- Modify: `README.zh-CN.md`
- Create: `MERGE_NOTES.md`

- [ ] Document the new route and ModLens v2 evidence contract.
- [ ] Run `gofmt` on changed Go files.
- [ ] Run targeted Go tests, then `go test ./...` and `go vet ./...` when the configured toolchain is available.
- [ ] Run available frontend tests/build when package manager dependencies are available.
- [ ] Record any environment-blocked checks exactly in `MERGE_NOTES.md`.
- [ ] Produce a clean ZIP without `.git`, build output, or caches.
