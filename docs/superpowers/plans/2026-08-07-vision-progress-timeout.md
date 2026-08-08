# Vision Progress And Timeout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make visual evidence extraction bounded and observable so users see connection, fallback, analysis, parsing, completion, and failure states instead of a long silent wait with one folded line.

**Architecture:** Keep ModLens v2 as the only model-facing output contract. Add a typed `vision_progress` event for host-authored lifecycle state, propagate it through `eventwire`, and keep the latest progress state in the frontend controller rather than appending every update to the transcript. Bound the entire visual operation, including retries and Agnes Responses-to-Chat fallback, with one shared deadline.

**Tech Stack:** Go event/provider/vision layers, Wails JSON event bridge, React + TypeScript frontend, existing Vitest-style `tsx` tests.

## Global Constraints

- The visual provider continues to come from the Reasonix-resolved provider in `%APPDATA%\\reasonix\\config.toml`; do not read MCP `vision-config.json`.
- Preserve ModLens Output Schema v2 strict validation, including the rejection of `bbox` and `confidence` fields.
- Do not expose raw model chain-of-thought in the UI. Show host-authored lifecycle stages and elapsed time instead.
- Do not include API keys, full request bodies, raw provider error bodies, or image data in progress events.
- All retry attempts for one visual operation share a 90-second total deadline; a retry must not reset the full timeout.
- Keep the existing `phase` event contract for unrelated planner/executor phases. Append any new wire kind after existing kinds to preserve numeric compatibility.

---

### Task 1: Add A Bounded Vision Operation Lifecycle

**Files:**
- Modify: `internal/vision/describer.go`
- Modify: `internal/vision/toolimages.go`
- Modify: `internal/control/image_router.go`
- Test: `internal/vision/describer_test.go`
- Test: `internal/vision/toolimages_test.go`
- Test: `internal/control/image_router_test.go`

**Interfaces:**
- `vision.ProviderDescriber` emits progress through a callback supplied by the caller.
- `control.routeImagesOnce` and `vision.ProviderToolImageProcessor.ProcessToolImages` create one total context for their retry loop.
- The existing `Describer` interface and ModLens evidence return values remain unchanged.

- [ ] **Step 1: Write failing timeout tests.**

  Add tests that use a fake describer/provider which blocks until its context is cancelled. Assert that the direct image route and tool-image route return within the configured total deadline, call no more than the existing maximum attempts, and do not give each attempt a fresh full timeout.

- [ ] **Step 2: Write failing lifecycle tests.**

  Add a fake sink and assert the direct route emits ordered stages equivalent to `preparing`, `connecting`, `waiting`, `parsing`, and `ready`; a context cancellation emits `cancelled`; and an exhausted deadline emits `failed` with a short safe detail.

- [ ] **Step 3: Implement shared deadlines.**

  Introduce explicit constants in `internal/vision` for a 90-second total visual-operation deadline and a single-request ceiling no longer than 90 seconds. Use the shorter of the caller deadline and the 90-second visual deadline. Pass this context through every retry instead of creating an independent full-duration context per attempt. Apply the same rule to `ProcessToolImages`.

- [ ] **Step 4: Implement backend lifecycle callbacks.**

  Emit `preparing` before image conversion, `connecting` immediately before `Provider.Stream`, `waiting` after the provider accepts the request, `parsing` before `ParseEvidence`, and terminal `ready`, `failed`, or `cancelled` states. Treat provider `ChunkReasoning` as internal data: never append it to the user-visible evidence text and never emit it as raw reasoning.

- [ ] **Step 5: Verify the focused Go tests.**

  Run:

  ```text
  go test ./internal/vision ./internal/control
  ```

  Expected: PASS, with the blocking fakes completing at the shared deadline rather than after three independent timeouts.

---

### Task 2: Surface Provider Fallback And Safe Progress Data

**Files:**
- Modify: `internal/provider/provider.go`
- Modify: `internal/provider/responses/responses.go`
- Modify: `internal/vision/describer.go`
- Test: `internal/provider/responses/json_test.go`
- Test: `internal/provider/provider_test.go`

**Interfaces:**
- Add a context-scoped provider progress reporter carrying a closed stage/detail vocabulary, not arbitrary provider text.
- `responses.client.Stream` reports `fallback` when Agnes Responses returns an eligible 400 and the existing Chat Completions fallback is selected.
- `ProviderDescriber` translates provider progress into the visual lifecycle callback.

- [ ] **Step 1: Write the fallback progress regression test.**

  Extend the existing Agnes fallback test in `internal/provider/responses/json_test.go` with a context progress collector. Make the Responses endpoint return 400 and Chat Completions return valid ModLens JSON. Assert that exactly one safe `fallback` progress record is emitted, the fallback request still carries the image parts, and the 400 response body is not included in the progress detail.

- [ ] **Step 2: Add the context reporter.**

  Define a provider progress type with only these stages: `fallback`. Add `WithProgressReporter` and `ReportProgress` helpers that safely no-op when no reporter is installed. Do not add progress fields to provider request JSON.

- [ ] **Step 3: Report the Agnes fallback.**

  Call the reporter immediately before `chat.Stream` in `responses.client.Stream`, with detail such as `Responses image request rejected; using Chat Completions fallback`. Keep the existing fallback behavior and error wrapping unchanged.

- [ ] **Step 4: Verify provider behavior.**

  Run:

  ```text
  go test ./internal/provider ./internal/provider/responses ./internal/provider/openai ./internal/vision
  ```

  Expected: PASS; existing request-shape tests remain green and no provider secret or body content appears in progress records.

---

### Task 3: Add The `vision_progress` Wire Contract

**Files:**
- Modify: `internal/event/event.go`
- Modify: `internal/eventwire/wire.go`
- Test: `internal/eventwire/wire_test.go`
- Test: `internal/event/event_test.go`

**Interfaces:**
- Append `VisionProgress` to `event.Kind` before `KindCount`.
- Add `event.VisionProgressInfo` with `Stage`, `ModelRef`, `Attempt`, `MaxAttempts`, and a bounded `Detail`.
- Add `Event.VisionProgress *VisionProgressInfo` and wire it as `visionProgress`.

- [ ] **Step 1: Write the wire contract tests.**

  Assert that `ToWire(event.Event{Kind: event.VisionProgress, VisionProgress: ...})` produces `kind: "vision_progress"` and preserves only the documented progress fields. Assert that the existing kind-name completeness test includes the appended kind without changing earlier numeric names.

- [ ] **Step 2: Implement the typed event.**

  Add the new kind and payload using stable stage strings: `preparing`, `connecting`, `waiting`, `fallback`, `parsing`, `ready`, `failed`, and `cancelled`. Add the wire payload conversion in `ToWire` and the frontend kind-name map.

- [ ] **Step 3: Connect the vision lifecycle to the event sink.**

  Have the controller/describer callback emit `event.Event{Kind: event.VisionProgress, Source: event.UsageSourceVision, VisionProgress: ...}`. Keep the existing final `Notice` detail for diagnostics, but use the new event for live status rather than relying on a one-line `Phase` event.

- [ ] **Step 4: Verify the event layer.**

  Run:

  ```text
  go test ./internal/event ./internal/eventwire ./internal/control ./internal/vision
  ```

  Expected: PASS, with round-trip coverage for progress stages and no changes to unrelated event JSON.

---

### Task 4: Render A Live Vision Progress Card In The Desktop UI

**Files:**
- Modify: `desktop/frontend/src/lib/types.ts`
- Modify: `desktop/frontend/src/lib/useController.ts`
- Modify: `desktop/frontend/src/components/Transcript.tsx`
- Modify: `desktop/frontend/src/App.tsx`
- Modify: `desktop/frontend/src/locales/zh.ts`
- Modify: `desktop/frontend/src/locales/en.ts`
- Modify: `desktop/frontend/src/locales/zh-TW.ts`
- Modify: `desktop/frontend/src/styles.css`
- Test: `desktop/frontend/src/__tests__/use-controller-vision-progress.test.ts`
- Test: `desktop/frontend/src/__tests__/vision-progress-card.test.tsx`

**Interfaces:**
- Add `WireVisionProgress` and `vision_progress` to the frontend event types.
- Add `visionProgress` to controller state as a single replaceable record keyed to the active turn; do not append progress updates as transcript notices.
- Pass the active progress record to `Transcript` and render `VisionProgressCard` beside the current process fold.

- [ ] **Step 1: Write reducer tests.**

  Dispatch `preparing`, `connecting`, `waiting`, `fallback`, and `parsing` events and assert that one record is updated in place. Dispatch `ready`, `failed`, `cancelled`, and `turn_done` and assert that the live card reaches a terminal state and cannot remain indefinitely active after the turn ends.

- [ ] **Step 2: Write rendering tests.**

  Render the card with each stage and assert that it shows localized stage text, the configured model reference, attempt count, and a live elapsed timer while active. Assert that fallback is visibly distinct from a normal wait and that failed/cancelled states stop the spinner.

- [ ] **Step 3: Implement state and event handling.**

  Extend `WireEvent`, reducer state, and `applyEvent` with a `vision_progress` case. Store `startedAt` locally when the first non-terminal stage arrives; use the existing tick pattern for elapsed time. Clear the state on `turn_done`, on a new `turn_started`, and when the active tab/runtime epoch changes.

- [ ] **Step 4: Implement the card.**

  Add a compact accessible card with `role="status"` and `aria-live="polite"`. Show a spinner only for active stages, a check icon for `ready`, and a warning/error icon for `failed` or `cancelled`. Use the existing Lucide icon and process-card styling conventions. Do not show raw provider response text or raw reasoning.

- [ ] **Step 5: Add localized copy and CSS.**

  Add stage labels for Simplified Chinese, English, and Traditional Chinese. Keep the copy concrete: `准备图片`, `连接供应商`, `等待视觉模型响应`, `切换兼容接口`, `解析 ModLens 证据`, `视觉证据已完成`, and `视觉证据失败`.

- [ ] **Step 6: Verify focused frontend tests.**

  Run from `desktop/frontend`:

  ```text
  pnpm exec tsx src/__tests__/use-controller-vision-progress.test.ts
  pnpm exec tsx src/__tests__/vision-progress-card.test.tsx
  pnpm typecheck
  ```

  Expected: PASS, with no TypeScript errors and no stale active card after completion or cancellation.

---

### Task 5: End-To-End Verification With The Reasonix Provider Config

**Files:**
- No production files created.
- Verify: `C:\Users\guojl\AppData\Roaming\reasonix\config.toml`
- Verify: `C:\Users\guojl\AppData\Roaming\reasonix\.env`

- [ ] **Step 1: Run the backend and frontend regression lanes.**

  Run:

  ```text
  go test ./internal/provider ./internal/provider/responses ./internal/provider/openai ./internal/vision ./internal/control ./internal/event ./internal/eventwire
  cd desktop/frontend
  pnpm test:typecheck
  pnpm exec tsx src/__tests__/use-controller-vision-progress.test.ts
  pnpm exec tsx src/__tests__/vision-progress-card.test.tsx
  pnpm build
  ```

  Expected: all commands exit with code 0.

- [ ] **Step 2: Run one real visual request.**

  Start the dev desktop with `REASONIX_DEV=1` and the configured Reasonix home. Submit the existing screenshot through the normal attachment path. Confirm that the UI shows the lifecycle card immediately, shows `fallback` when Agnes rejects `/responses`, then shows parsing and completion; if the network is unavailable, it shows a failure state within the shared deadline and releases the composer.

- [ ] **Step 3: Verify cancellation and timeout recovery.**

  Cancel during `waiting` and verify the card becomes cancelled, the turn emits `turn_done`, and a second turn can be submitted. Repeat with a local test endpoint that never sends response headers and verify the 90-second total deadline is enforced.

- [ ] **Step 4: Verify ModLens compatibility and privacy.**

  Confirm the final model input still contains exactly one `<visual-evidence schema="modlens-v2">` block, strict schema validation still rejects forbidden fields, and frontend/backend logs contain no API key, image data URL, or raw provider response body.

**Plan self-review:** Both reported symptoms are covered: the shared deadline prevents multi-attempt silent waits, and the structured progress event plus live card replaces the current single folded phase line. The ModLens input/output contract, provider source, cancellation, timeout, fallback, localization, and privacy requirements are covered by explicit tasks and tests.
