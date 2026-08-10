# Vision Duration and Capability Routing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep visual-stage durations monotonic and durable across completion/cancellation, and invoke the independent visual model automatically only when the selected main model cannot accept image input.

**Architecture:** Keep Go `duration_ms` values authoritative, while making the React reducer preserve locally elapsed time when progress events omit the new timing fields or arrive from a stale backend. Move image-capability routing ahead of independent-vision routing for user attachments and tool-returned images, while leaving explicit tools and MCP capabilities available.

**Tech Stack:** Go controller/vision/event packages, Wails event wire, React 19 + TypeScript reducer, Go tests, pnpm/tsx tests.

## Global Constraints

- Do not change the four provider request protocols or their image field mappings.
- Do not change the ModLens v2 schema or evidence rendering.
- Do not disable or hide MCP tools; an explicit user/tool invocation remains available.
- Main-model image capability comes from Reasonix provider configuration (`vision_models`, model override, or effective provider capability).
- Legacy history containing only cumulative `elapsed_ms` must not be presented as exact per-stage duration.
- Delivery verification must use a freshly rebuilt Go/Wails desktop process, not frontend HMR alone.

---

### Task 1: Preserve Live Stage Time Across Sparse or Legacy Events

**Files:**
- Modify: `desktop/frontend/src/__tests__/use-controller-vision-progress.test.ts`
- Modify: `desktop/frontend/src/lib/useController.ts`

**Interfaces:**
- Consumes: `WireVisionProgress`, vision item `liveUpdatedAt`, `liveStageKey`.
- Produces: monotonic `analysis.elapsed_ms` and `stage.duration_ms` even when `stageElapsedMs` and `completedStageElapsedMs` are absent.

- [ ] **Step 1: Add a failing legacy-event reducer test**

Override `Date.now`, emit `preparing -> connecting -> connecting -> failed` without any new timing fields, and assert the connecting stage grows to six seconds and retains that value after failure:

```ts
const originalNow = Date.now;
let now = 1_000;
Date.now = () => now;
try {
  let legacy = reducer(initialState, { type: "user", text: "legacy timing", seq: initialState.seq });
  legacy = event(legacy, visionEvent({ analysisId: "legacy-live", attempt: 1, stage: "preparing" }));
  now = 1_100;
  legacy = event(legacy, visionEvent({ analysisId: "legacy-live", attempt: 1, stage: "connecting" }));
  now = 5_100;
  legacy = event(legacy, visionEvent({ analysisId: "legacy-live", attempt: 1, stage: "connecting" }));
  now = 7_100;
  legacy = event(legacy, visionEvent({ analysisId: "legacy-live", attempt: 1, stage: "failed" }));
  eq(visionItems(legacy)[0]?.analysis.stages.find((stage) => stage.stage === "connecting")?.duration_ms, 6_000, "legacy progress keeps a monotonic stage duration");
} finally {
  Date.now = originalNow;
}
```

- [ ] **Step 2: Verify RED**

Run:

```powershell
Set-Location desktop/frontend
pnpm exec tsx src/__tests__/use-controller-vision-progress.test.ts
```

Expected: the new duration assertion fails because each same-stage event resets `liveUpdatedAt` without first folding local elapsed time into the durable reducer state.

- [ ] **Step 3: Fold the previous live interval before every update**

Add a reducer helper that, when the previous vision item is active, computes `Date.now() - liveUpdatedAt`, adds it to the stage identified by `liveStageKey`, and advances total `analysis.elapsed_ms`. Apply this snapshot before merging incoming backend timing fields. Use one `now` value for the whole reducer update.

- [ ] **Step 4: Clear live metadata only after terminal folding**

For `ready`, `failed`, and `cancelled`, preserve the folded stage duration on `analysis.stages` before setting `liveUpdatedAt` and `liveStageKey` to `undefined`.

- [ ] **Step 5: Verify GREEN**

Run the command from Step 2 and expect all assertions to pass.

---

### Task 2: Route User Images Directly to an Image-Capable Main Model

**Files:**
- Modify: `internal/control/image_router_test.go`
- Modify: `internal/control/media_vision_test.go`
- Modify: `internal/control/image_router.go`
- Modify: `internal/control/media_router.go`

**Interfaces:**
- Consumes: `Controller.mainModelSupportsVision()` and `ImageRouteState.RequireIndependentVision`.
- Produces: `ImageRouteDirectMain` before any independent describer call for ordinary media when the main model supports images.

- [ ] **Step 1: Replace the failing-policy test**

Change `TestRouteImagesPrefersConfiguredVisionEvidenceForVisionCapableMainModel` into a test that expects:

```go
if res.Mode != ImageRouteDirectMain || len(res.Images) != 1 || d.calls != 0 {
    t.Fatalf("res=%+v calls=%d", res, d.calls)
}
```

Add a `routeResolvedMediaOnce` assertion that an image-capable main model receives no `<visual-model-assistance>` or `<visual-reanalysis-request>` automatic instruction.

- [ ] **Step 2: Verify RED**

Run:

```powershell
go test ./internal/control -run 'RouteImages.*VisionCapableMain|VisualModelAssistance.*VisionCapable|Reanalysis.*VisionCapable' -count=1
```

Expected: the direct-route assertion fails because `routeImagesOnce` currently invokes the configured independent describer first.

- [ ] **Step 3: Move the direct-main decision before independent vision**

Immediately after image validation in `routeImagesOnce`, return `ImageRouteDirectMain` when `mainModelSupportsVision()` is true and `RequireIndependentVision` is false. Resolve and call the independent visual model only for text-only main models or explicit independent-tool execution paths.

- [ ] **Step 4: Stop automatic independent-vision prompt injection for capable main models**

Make `injectVisualModelAssistanceWithTool` return the unmodified input when the main model supports images. In `routeResolvedMediaOnce`, only apply independent reanalysis guidance automatically for text-only main models. Keep the registered first-party visual tool and MCP tools available for explicit model/user calls.

- [ ] **Step 5: Verify GREEN**

Run the command from Step 2 and expect PASS.

---

### Task 3: Preserve Tool-Returned Images for an Image-Capable Main Model

**Files:**
- Modify: `internal/vision/toolimages_test.go`
- Modify: `internal/vision/toolimages.go`
- Verify: `internal/agent/agent_toolimages_bridge_test.go`

**Interfaces:**
- Consumes: `ToolImageInput.ModelSupportsImages`.
- Produces: unchanged tool text and provider-visible raw images without an independent visual analysis record when the main model accepts images.

- [ ] **Step 1: Rewrite the capable-main processor test**

Expect the configured independent describer not to run:

```go
if d.calls != 0 || len(out.Images) != 1 || out.Text != "ok" || len(out.VisualAnalyses) != 0 {
    t.Fatalf("out=%+v calls=%d", out, d.calls)
}
```

- [ ] **Step 2: Verify RED**

Run:

```powershell
go test ./internal/vision -run 'ToolImageProcessor.*VisionCapableMain' -count=1
```

Expected: FAIL because the processor currently prefers configured visual evidence even when `ModelSupportsImages` is true.

- [ ] **Step 3: Add the capability-first return**

At the beginning of `ProcessToolImages`, after the empty-image check, return `ToolImageOutput{Text: in.ToolText, Images: in.Images}` when `in.ModelSupportsImages` is true. Leave text-only ModLens extraction, retries, failure messages, and local image retention unchanged.

- [ ] **Step 4: Verify GREEN and agent propagation**

Run:

```powershell
go test ./internal/vision ./internal/agent -run 'ToolImage|VisualAnalysis' -count=1
```

Expected: PASS.

---

### Task 4: Contract, History, and Desktop Verification

**Files:**
- Verify: `internal/vision/analysis_record_test.go`
- Verify: `internal/eventwire/vision_progress_test.go`
- Verify: `desktop/frontend/src/__tests__/vision-process-item.test.tsx`
- Verify all modified files.

**Interfaces:**
- Confirms backend `duration_ms` persistence, event-wire timing fields, frontend terminal retention, and capability-first routing.

- [ ] **Step 1: Run focused regressions**

```powershell
go test ./internal/eventwire ./internal/vision ./internal/control ./internal/agent -run 'Vision|Visual|ToolImage|RouteImages|Reanalysis' -count=1
Set-Location desktop/frontend
pnpm exec tsx src/__tests__/use-controller-vision-progress.test.ts
pnpm exec tsx src/__tests__/vision-process-item.test.tsx
```

Expected: PASS.

- [ ] **Step 2: Format and run project checks**

```powershell
gofmt -w internal/control/image_router.go internal/control/image_router_test.go internal/control/media_router.go internal/control/media_vision_test.go internal/vision/toolimages.go internal/vision/toolimages_test.go
go vet ./...
go run ./tools/repolint
go test ./internal/tool/builtin/ ./internal/boot/
Set-Location desktop/frontend
pnpm typecheck
pnpm build
```

Expected: all commands exit 0; an unchanged pre-existing repolint budget failure may be reported separately but must not increase.

- [ ] **Step 3: Fully restart the desktop development runtime**

Terminate the stale `reasonix-desktop-dev.exe`, its Wails process, and the project Vite child process. Start a fresh `wails dev` from `desktop/`, then verify the new desktop executable process start time is later than the implementation build time.

- [ ] **Step 4: Verify with local Reasonix configuration**

Using the existing `config.toml` without exposing keys:

- select `tabitoken/claude-opus-4-8`, attach an image, and confirm no automatic `Grok本地/grok-4.5` visual process appears;
- select a configured text-only main model, attach an image, and confirm the independent visual process appears;
- complete and cancel visual analyses, then confirm every stage duration remains visible;
- reopen the conversation and confirm new history records contain `duration_ms`.

- [ ] **Step 5: Review the final diff**

Confirm there are no provider protocol payload changes, ModLens schema changes, MCP suppression, key/config edits, generated frontend assets, or unrelated modifications.
