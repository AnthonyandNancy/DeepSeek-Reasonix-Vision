# Vision Timeline Anchoring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep every live and replayed visual-model process directly attached to the user message or tool call that caused it.

**Architecture:** Add local-only owner metadata to visual progress events, propagate tool-call identity through execution context, and make the frontend insert timeline items relative to explicit owners. Preserve the startup assistant cursor as a movable empty placeholder so it cannot precede work that actually happened first.

**Tech Stack:** Go event/tool/agent/vision packages, Wails event wire, React 19 + TypeScript reducer and transcript, pnpm/tsx tests.

## Global Constraints

- Do not change provider request payloads, four-protocol transport behavior, ModLens v2 evidence, or MCP routing.
- Keep `analysisId` as the stable merge key for one visual lifecycle and its retries.
- Preserve the startup assistant loading cursor.
- Live and history replay must produce the same item order.
- New local metadata must be bounded and must not expose provider payloads or API keys.

---

### Task 1: Reproduce Live Timeline Failures

**Files:**
- Modify: `desktop/frontend/src/__tests__/use-controller-vision-progress.test.ts`
- Modify: `desktop/frontend/src/__tests__/transcript-process-fold.test.ts`

**Interfaces:**
- Consumes: `reducer`, `initialState`, `historyMessagesToItems`.
- Produces: exact sequence assertions for host-auto, tool-owned, and parallel-tool visual events.

- [ ] **Step 1: Add a host-auto reducer test**

Drive `user -> turn_started -> vision_progress -> reasoning -> message` and
assert `user > vision > assistant`. Compare it with hydrated history containing
the same analysis and assert the kind order is identical.

- [ ] **Step 2: Add a no-reasoning tool test**

Drive `user -> turn_started -> tool_dispatch -> vision_progress(owner=tool) ->
tool_result -> text -> message` and assert `user > tool > vision > assistant`.

- [ ] **Step 3: Add a parallel-tool ownership test**

Create tool `image-tool`, then `text-tool`, and emit visual progress owned by
`image-tool`. Assert the visual item is between those tools.

- [ ] **Step 4: Verify RED**

Run:

```powershell
pnpm exec tsx src/__tests__/use-controller-vision-progress.test.ts
```

Expected: the three new order assertions fail against the current append-only
reducer.

---

### Task 2: Add Visual Owner Metadata

**Files:**
- Create: `internal/tool/invocation_context.go`
- Create: `internal/tool/invocation_context_test.go`
- Modify: `internal/agent/agent.go`
- Modify: `internal/event/event.go`
- Modify: `internal/eventwire/wire.go`
- Modify: `internal/eventwire/vision_progress_test.go`
- Modify: `internal/vision/analysis_record.go`
- Modify: `internal/vision/analysis_record_test.go`
- Modify: `internal/vision/analyze_tool.go`
- Modify: `internal/vision/analyze_tool_test.go`
- Modify: `internal/vision/toolimages.go`
- Modify: `internal/vision/toolimages_test.go`

**Interfaces:**
- Produces: `tool.WithInvocationID(context.Context, string) context.Context`.
- Produces: `tool.InvocationID(context.Context) string`.
- Produces wire fields `ownerKind` and `ownerId` on `visionProgress`.

- [ ] **Step 1: Add failing Go tests**

Assert that invocation identity round-trips through context, progress scope
copies owner fields to every emitted event, wire JSON carries those fields,
explicit visual-tool events use the executing tool ID, and tool-image events use
`ToolImageInput.ToolCallID`.

- [ ] **Step 2: Verify RED**

Run:

```powershell
go test ./internal/tool ./internal/eventwire ./internal/vision -run 'InvocationID|VisionProgress|AnalyzeMediaTool.*Owner|ToolImageProcessor.*Owner' -count=1
```

Expected: compile failures for the missing context helpers and owner fields.

- [ ] **Step 3: Implement invocation context**

Add a private context key plus trimming helpers in
`internal/tool/invocation_context.go`. Update `agent.withCallContext` to wrap the
context with `tool.WithInvocationID(ctx, parentID)` before storing agent-specific
call state.

- [ ] **Step 4: Extend progress and wire contracts**

Add `OwnerKind` and `OwnerID` to `event.VisionProgressInfo`,
`vision.ProgressScope`, `eventwire.VisionProgress`, and the TypeScript
`WireVisionProgress` shape. `emitProgressEvent` copies scope ownership onto each
event alongside analysis ID, initiator, media count, and attempt.

- [ ] **Step 5: Populate owners at every visual entry point**

- Host image routing uses `OwnerKind: "user"`.
- `analyze_media_with_vision` uses `OwnerKind: "tool"` and
  `tool.InvocationID(ctx)`.
- Tool-image processing uses `OwnerKind: "tool"` and
  `ToolImageInput.ToolCallID`.

- [ ] **Step 6: Verify GREEN**

Run the focused Go command from Step 2 and expect PASS.

---

### Task 3: Anchor Frontend Timeline Items

**Files:**
- Modify: `desktop/frontend/src/lib/types.ts`
- Modify: `desktop/frontend/src/lib/useController.ts`
- Test: `desktop/frontend/src/__tests__/use-controller-vision-progress.test.ts`

**Interfaces:**
- Produces visual item fields `ownerKind?: "user" | "tool"` and `ownerId?: string`.
- Produces one timeline insertion helper used by new tool and vision items.

- [ ] **Step 1: Add owner fields to frontend types**

Mirror `ownerKind` and `ownerId` on `WireVisionProgress`. Store normalized owner
metadata on each visual `Item`.

- [ ] **Step 2: Implement movable-placeholder insertion**

Create a helper that detects `currentAssistant` only while both its stored and
live text/reasoning are empty. Unanchored process items insert immediately before
that assistant. Replace new-tool append operations with this helper.

- [ ] **Step 3: Implement owner-relative visual insertion**

On the first event for an analysis:

- normalize owner fields, falling back from `host_auto` to `user`;
- resolve user ownership to the latest user item;
- insert after the owner and existing visual siblings with the same owner;
- otherwise insert before the untouched assistant placeholder.

On later events, update the existing item in place. When a new tool card arrives,
re-anchor any earlier visual item whose `ownerId` matches the tool ID.

- [ ] **Step 4: Preserve ownership during history replay**

Pass the created user or tool item ID into `appendHistoryVisualAnalyses` and set
the visual item's owner fields while retaining the existing durable record.

- [ ] **Step 5: Verify GREEN**

Run:

```powershell
pnpm exec tsx src/__tests__/use-controller-vision-progress.test.ts
```

Expected: all original lifecycle checks and new ordering checks pass.

---

### Task 4: Render and Integration Regression Coverage

**Files:**
- Modify: `desktop/frontend/src/__tests__/transcript-process-fold.test.ts`
- Modify: `internal/control/turn_orchestrator_vision_test.go`
- Modify: `internal/agent/agent_toolimages_bridge_test.go`

**Interfaces:**
- Verifies event stream -> reducer items -> DOM order.
- Verifies owner metadata survives controller and tool-image execution paths.

- [ ] **Step 1: Add reducer-backed DOM assertions**

Render items created from the real host-auto reducer sequence and assert the
visual process follows the user and precedes the assistant answer in standard
and compact modes.

- [ ] **Step 2: Cover retries and terminal stages**

Emit retry, failure, and cancellation updates for one `analysisId`; assert one
visual item remains at its original owner anchor.

- [ ] **Step 3: Cover multi-image and parallel-tool paths**

Assert `mediaCount > 1` does not create duplicate visual items and a tool-media
record remains attached to its exact tool call even when another read-only tool
completes first or later.

- [ ] **Step 4: Run focused integration tests**

```powershell
go test ./internal/tool ./internal/eventwire ./internal/vision ./internal/agent ./internal/control -run 'Vision|Visual|InvocationID|ToolImage' -count=1
pnpm exec tsx src/__tests__/use-controller-vision-progress.test.ts
pnpm exec tsx src/__tests__/transcript-process-fold.test.ts
```

Expected: PASS with no warnings.

---

### Task 5: Final Project Verification

**Files:**
- Verify all modified files.

**Interfaces:**
- Produces a formatted, lint-clean, regression-tested patch.

- [ ] **Step 1: Format and static-check**

```powershell
gofmt -w internal/tool/invocation_context.go internal/tool/invocation_context_test.go internal/agent/agent.go internal/event/event.go internal/eventwire/wire.go internal/eventwire/vision_progress_test.go internal/vision/analysis_record.go internal/vision/analysis_record_test.go internal/vision/analyze_tool.go internal/vision/analyze_tool_test.go internal/vision/toolimages.go internal/vision/toolimages_test.go internal/control/turn_orchestrator_vision_test.go internal/agent/agent_toolimages_bridge_test.go
go vet ./...
go run ./tools/repolint
pnpm typecheck
```

Expected: every command exits 0.

- [ ] **Step 2: Run regression suites**

```powershell
go test ./internal/tool ./internal/eventwire ./internal/vision ./internal/agent ./internal/control -count=1
pnpm exec tsx src/__tests__/use-controller-vision-progress.test.ts
pnpm exec tsx src/__tests__/transcript-process-fold.test.ts
```

Expected: PASS.

- [ ] **Step 3: Review the final diff**

Confirm the patch contains no provider request changes, protocol branches,
ModLens schema changes, MCP suppression, generated bundles, or unrelated files.

