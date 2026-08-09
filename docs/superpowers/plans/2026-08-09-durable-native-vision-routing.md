# Durable Native Vision Routing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist every independent-vision analysis in the conversation, render its live and replayed lifecycle with Reasonix's native process UI, and make explicit historical re-analysis a first-party tool call initiated by the main model.

**Architecture:** Provider-visible ModLens v2 evidence remains in ordinary user/tool message content. A bounded `provider.VisualAnalysisRecord` is stored beside that message as provider-excluded transcript metadata. Runtime `vision_progress` events upsert the same analysis ID into the frontend `Item[]`, while history replay reconstructs identical `vision` items. New attachments keep the low-latency host pre-processing path; explicit re-analysis is performed through a root-session `analyze_media_with_vision` tool visible to the main model.

**Tech Stack:** Go controller/agent/provider/vision packages, Reasonix session JSONL/event-log persistence, Wails desktop wire structs, React 19 + TypeScript transcript UI, pnpm/tsx tests.

## Global Constraints

- The visual model is resolved only from `C:\Users\guojl\AppData\Roaming\reasonix\config.toml`; `vision-config.json` remains MCP-only and must not be read by this feature.
- Preserve the four existing provider protocols without Agnes-specific branches.
- Every model-visible visual result must follow ModLens Output Schema v2; do not introduce `bbox` or numeric confidence fields.
- Do not disable, hide, reroute, or specially warn about MCP tools. Explicit MCP requests and existing MCP automatic behavior remain unchanged.
- The installed official Reasonix application must not be modified; development continues from this repository checkout.
- Persist only bounded visual response/reasoning data and never log or expose API keys.
- User-facing strings must follow the configured Simplified Chinese, Traditional Chinese, or English locale.

---

### Task 1: Provider-Excluded Visual Analysis Transcript Contract

**Files:**
- Modify: `internal/provider/provider.go`
- Modify: `internal/provider/provider_test.go`
- Modify: `internal/agent/session_events_test.go`

**Interfaces:**
- Produces: `provider.VisualAnalysisRecord`, `provider.VisualAnalysisStage`, and `Message.VisualAnalyses`.
- Invariant: `provider.ModelMessages` removes `VisualAnalyses`, while session save/load and event-log replay preserve it verbatim.

- [ ] **Step 1: Write failing provider tests**

```go
func TestModelMessagesStripsVisualAnalysisMetadata(t *testing.T) {
	input := []Message{{Role: RoleUser, Content: "visible evidence", VisualAnalyses: []VisualAnalysisRecord{{ID: "vision-1"}}}}
	got := ModelMessages(input)
	if len(got[0].VisualAnalyses) != 0 || got[0].Content != "visible evidence" {
		t.Fatalf("model messages leaked local visual metadata: %+v", got[0])
	}
}
```

- [ ] **Step 2: Run the focused test and verify RED**

Run: `go test ./internal/provider -run VisualAnalysis -count=1`

Expected: compile failure because the visual-analysis types and message field do not exist.

- [ ] **Step 3: Add the bounded transcript types**

```go
type VisualAnalysisStage struct {
	Attempt        int    `json:"attempt,omitempty"`
	Stage          string `json:"stage"`
	Response       string `json:"response,omitempty"`
	Reasoning      string `json:"reasoning,omitempty"`
	Detail         string `json:"detail,omitempty"`
	ElapsedMs      int64  `json:"elapsed_ms,omitempty"`
}

type VisualAnalysisRecord struct {
	ID          string                `json:"id"`
	Initiator   string                `json:"initiator"`
	ModelRef    string                `json:"model_ref,omitempty"`
	Status      string                `json:"status"`
	MediaRefs   []string              `json:"media_refs,omitempty"`
	MediaCount  int                   `json:"media_count,omitempty"`
	Stages      []VisualAnalysisStage `json:"stages,omitempty"`
	Summary     string                `json:"summary,omitempty"`
	OCRText     string                `json:"ocr_text,omitempty"`
	Evidence    string                `json:"evidence,omitempty"`
	StartedAt   int64                 `json:"started_at,omitempty"`
	CompletedAt int64                 `json:"completed_at,omitempty"`
	ElapsedMs   int64                 `json:"elapsed_ms,omitempty"`
}
```

Add `VisualAnalyses []VisualAnalysisRecord` to `provider.Message`, include it in the `ModelMessages` copy trigger, and clear it on the provider-bound copy.

- [ ] **Step 4: Add save/load replay coverage**

Create a session containing a user message with one visual record, save it, reload it, and assert ID, summary, OCR, stages, and initiator survive both the primary transcript and native event-log replay.

- [ ] **Step 5: Run focused tests and verify GREEN**

Run: `go test ./internal/provider ./internal/agent -run 'VisualAnalysis|SessionEventLogPreservesVisual' -count=1`

Expected: PASS.

---

### Task 2: Shared Progress Trace and Durable Records for Existing Vision Paths

**Files:**
- Create: `internal/vision/analysis_record.go`
- Create: `internal/vision/analysis_record_test.go`
- Modify: `internal/vision/describer.go`
- Modify: `internal/vision/toolimages.go`
- Modify: `internal/vision/toolimages_test.go`
- Modify: `internal/event/event.go`
- Modify: `internal/eventwire/wire.go`
- Modify: `internal/eventwire/vision_progress_test.go`
- Modify: `internal/control/image_router.go`
- Modify: `internal/control/image_router_test.go`
- Modify: `internal/agent/agent.go`
- Modify: `internal/agent/run_loop.go`
- Modify: `internal/agent/tool_images.go`
- Modify: `internal/agent/agent_toolimages_bridge_test.go`
- Modify: `internal/control/controller.go`
- Modify: `internal/control/turn_orchestrator.go`

**Interfaces:**
- Produces: `vision.NewAnalysisID`, `vision.NewAnalysisRecorder`, `vision.WithProgressScope`, and `ImageRouteResult.VisualAnalyses`.
- Produces: `agent.WithUserVisualAnalyses` for attaching host pre-analysis metadata to the persisted user turn.
- Progress wire fields: `analysisId`, `initiator`, `attempt`, and `mediaCount`.

- [ ] **Step 1: Write failing trace tests**

The test must emit `preparing → response(delta A) → thinking(delta B) → response(delta C) → ready` through one progress scope and assert one ordered record with merged response `AC`, reasoning `B`, one attempt, terminal status `ready`, and bounded fields.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/vision -run AnalysisRecorder -count=1`

Expected: compile failure because the recorder does not exist.

- [ ] **Step 3: Implement scoped progress recording**

```go
type ProgressScope struct {
	AnalysisID string
	Initiator  string
	MediaCount int
	Observe    func(event.VisionProgressInfo)
}

func WithProgressScope(ctx context.Context, scope ProgressScope) context.Context
func NewAnalysisRecorder(id, initiator, modelRef string, mediaRefs []string, mediaCount int) *AnalysisRecorder
func (r *AnalysisRecorder) Snapshot(evidence Evidence, renderedEvidence string) provider.VisualAnalysisRecord
```

The scope increments `Attempt` on each `preparing`, enriches every emitted event, and notifies the recorder. Response, reasoning, detail, evidence, and media refs must be copied and bounded.

- [ ] **Step 4: Route existing describer events through the scope**

Change describer progress emission to accept `ctx`, remove the controller's duplicate preparing event, and let each retry start a new attempt through the describer's own `preparing` event.

- [ ] **Step 5: Persist direct attachment analyses**

Add `VisualAnalyses []provider.VisualAnalysisRecord` to `ImageRouteResult`. Wrap each host pre-analysis call in a progress scope, snapshot success/failure, pass records through `agent.WithUserVisualAnalyses`, and store them on the user message in `beginRunTurn`.

- [ ] **Step 6: Persist automatic tool-image analyses**

Extend `vision.ToolImageOutput` and the agent tool-image bridge to return visual records. Attach those records to the corresponding `provider.RoleTool` message without changing its model-visible output or image behavior.

- [ ] **Step 7: Verify GREEN**

Run: `go test ./internal/vision ./internal/control ./internal/agent ./internal/eventwire -run 'Vision|VisualAnalysis|ToolImage' -count=1`

Expected: PASS.

---

### Task 3: Main-Model First-Party Historical Re-analysis Tool

**Files:**
- Create: `internal/vision/analyze_tool.go`
- Create: `internal/vision/analyze_tool_test.go`
- Modify: `internal/tool/tool.go`
- Modify: `internal/agent/execute_one.go`
- Modify: `internal/agent/agent.go`
- Modify: `internal/agent/run_loop.go`
- Modify: `internal/agent/task.go`
- Modify: `internal/control/media_router.go`
- Modify: `internal/control/media_vision_test.go`
- Modify: `internal/control/image_router.go`
- Modify: `internal/boot/boot.go`
- Modify: `internal/boot/boot_test.go`

**Interfaces:**
- Produces root-session tool `analyze_media_with_vision`.
- Tool arguments: optional `selection` (`latest` or `all`), optional one-based `image_index`, and optional `instruction`; `selection=all` and `image_index` are mutually exclusive.
- Produces optional tool execution interface returning `[]provider.VisualAnalysisRecord` alongside model-visible text.
- Controller resolver consumes only conversation-owned historical media; arbitrary filesystem paths are not accepted.

- [ ] **Step 1: Write failing tool contract tests**

Test the exact tool name, read-only status, schema, invalid mutually-exclusive selection, successful latest-image analysis, no-media error, and returned ModLens v2 evidence plus a `main_model_tool` visual record.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/vision -run AnalyzeMediaTool -count=1`

Expected: compile failure because the tool does not exist.

- [ ] **Step 3: Add structured transcript-metadata execution**

```go
type TranscriptMetadataResult struct {
	Output          string
	Images          []string
	VisualAnalyses  []provider.VisualAnalysisRecord
}

type TranscriptMetadataExecutor interface {
	ExecuteWithTranscriptMetadata(context.Context, json.RawMessage) (TranscriptMetadataResult, error)
}
```

Extend `toolOutcome` and `batchExecution` so visual metadata returned by a tool is persisted on its tool-result message. Do not add it to provider event payloads or model requests.

- [ ] **Step 4: Implement the visual tool and safe resolver**

```go
type MediaSelection struct {
	All   bool
	Index int // zero-based, -1 means latest
}

type MediaResolver func(context.Context, MediaSelection) ([]Image, []string, error)

func NewAnalyzeMediaTool(modelRef string, describer Describer, resolve MediaResolver) tool.Tool
```

The implementation resolves media first, creates a `main_model_tool` progress scope, calls the configured describer, validates ModLens v2 through the existing parser, and returns rendered evidence.

- [ ] **Step 5: Change re-analysis routing**

`resolveMediaForTurn` must continue resolving newly attached images, but an explicit historical re-analysis request must no longer call `resolveHistoricalMedia` or `routeImagesOnce` before the main model. Inject a short capability block telling the main model to call `analyze_media_with_vision` for a fresh analysis.

- [ ] **Step 6: Register the tool only when the configured visual model is usable**

Register it in `boot.Build` using the same `visionModelRef` and `visionDescriber` created from `config.toml`. The resolver closure loads the completed root controller and calls an exported safe historical-media resolver. Keep the tool out of spawned subagent registries while allowing the root planner/executor surfaces to retain it.

- [ ] **Step 7: Verify routing and MCP non-interference**

Tests must assert that an explicit `重新分析` turn reaches the first main-model request without any prior describer call, the tool remains present, and existing MCP registrations are neither removed nor renamed.

Run: `go test ./internal/vision ./internal/control ./internal/agent ./internal/boot -run 'AnalyzeMedia|Reanalysis|Subagent.*Vision|MCP' -count=1`

Expected: PASS.

---

### Task 4: Native Transcript Vision Process with History Replay

**Files:**
- Modify: `desktop/app.go`
- Modify: `desktop/history_test.go`
- Modify: `desktop/frontend/src/lib/types.ts`
- Modify: `desktop/frontend/src/lib/useController.ts`
- Modify: `desktop/frontend/src/components/Transcript.tsx`
- Modify: `desktop/frontend/src/styles.css`
- Modify: `desktop/frontend/src/locales/en.ts`
- Modify: `desktop/frontend/src/locales/zh.ts`
- Modify: `desktop/frontend/src/locales/zh-TW.ts`
- Modify: `desktop/frontend/src/__tests__/use-controller-vision-progress.test.ts`
- Modify: `desktop/frontend/src/__tests__/transcript-process-fold.test.ts`
- Replace: `desktop/frontend/src/__tests__/vision-progress-card.test.tsx`

**Interfaces:**
- Adds `visualAnalyses` to desktop history messages.
- Adds frontend `VisualAnalysisRecord`, `VisualAnalysisStage`, and `Item` variant `{ kind: "vision"; ... }`.
- Live `vision_progress` events upsert by `analysisId`; history replay inserts records after their user/tool owner.

- [ ] **Step 1: Write failing reducer tests**

Assert that progress for one analysis ID creates one `vision` item, repeated deltas merge into it, a second analysis ID creates a second item, terminal records remain after optimistic user submission and `turn_started`, and history hydration restores visual items.

- [ ] **Step 2: Verify RED**

Run: `pnpm exec tsx src/__tests__/use-controller-vision-progress.test.ts`

Expected: assertion failure because the current reducer clears global visual state.

- [ ] **Step 3: Replace global progress state with transcript items**

Remove `State.visionProgress`, `State.visionProgressHistory`, the corresponding `Transcript` props, and their reset logic. Upsert a `vision` item in chronological `items` order for each `analysisId`.

- [ ] **Step 4: Restore records from Go history**

Add `VisualAnalyses []provider.VisualAnalysisRecord` to `HistoryMessage`. Copy records from user and tool-result provider messages. In `historyMessagesToItems`, insert user-owned analyses immediately after the user item and tool-owned analyses immediately after the tool item.

- [ ] **Step 5: Render vision through `TurnCollapse`**

Include `vision` in `displayItems`, running-state detection, process counts, duration calculation, and the body switch. Replace `VisionProgressCard` with `VisionProcessItem`, reusing `turn-collapse__reasoning-head`, `turn-collapse__inline-reasoning`, native shimmer, duration, icon, spacing, and fold behavior.

- [ ] **Step 6: Add truthful localized copy**

Provide locale keys for `Visual analysis`, `Visual analysis in progress`, `Application automatically processed media`, `Main model requested visual analysis`, `Application tool media bridge`, stage labels, summary, OCR, response, reasoning, and model. Do not display fabricated reasoning when none was emitted.

- [ ] **Step 7: Verify GREEN**

Run: `pnpm exec tsx src/__tests__/use-controller-vision-progress.test.ts`

Run: `pnpm exec tsx src/__tests__/transcript-process-fold.test.ts`

Run: `pnpm typecheck`

Expected: PASS.

---

### Task 5: Integration, Regression, and Development Build Verification

**Files:**
- Modify only if a failing verification exposes a defect in the preceding tasks.

**Interfaces:**
- Consumes all prior task outputs.
- Produces verification evidence for persistence, main-model initiation, native rendering, ModLens v2, and MCP non-interference.

- [ ] **Step 1: Run focused backend tests**

Run: `go test ./internal/provider ./internal/eventwire ./internal/vision ./internal/control ./internal/agent ./internal/boot ./desktop -count=1`

- [ ] **Step 2: Run project lint guards**

Run: `gofmt -w .`

Run: `go vet ./...`

Run: `go run ./tools/repolint`

- [ ] **Step 3: Run frontend validation**

Run: `pnpm test:typecheck`

Run: `pnpm build`

- [ ] **Step 4: Run full Go suite**

Run: `go test ./... -count=1`

- [ ] **Step 5: Local configuration integration test**

Using the existing development Reasonix profile and its `config.toml`, test without printing the API key:

1. Send one and multiple new images; host pre-analysis appears below the user turn and persists after another message.
2. Restart/reopen the session; visual summary/OCR/process remains searchable and visible.
3. Send `重新分析上一张图片`; the main model emits reasoning/tool dispatch first, then `analyze_media_with_vision` runs, then the main model continues from ModLens v2 evidence.
4. Verify a configured vision MCP remains available and is still used when explicitly requested.
5. Verify Simplified Chinese, Traditional Chinese, and English labels follow the application language.

- [ ] **Step 6: Inspect the final diff and run a whole-branch code review**

Confirm no key, endpoint credential, `vision-config.json` dependency, Agnes-specific branch, or unrelated installed-application file appears in the diff.
