# Vision Protocol Compatibility Implementation Plan

**Goal:** Make the provider layer handle OpenAI Chat Completions, OpenAI Responses, Anthropic Messages, and compatible JSON/SSE responses without weakening the ModLens v2 visual-evidence contract.

**Architecture:** Keep `provider.Request` transport-neutral. Each provider serializes image parts and parses its own streaming or non-streaming response. The vision describer remains the ModLens boundary: it sends the fixed extractor prompt, accepts only `vision.ParseEvidence` output, and retries bounded transport/schema failures.

**Tech Stack:** Go, `net/http`, JSON/SSE parsing, existing provider and vision test fixtures, Wails frontend regression tests.

## Global Constraints

- Preserve `internal/vision` ModLens v2 required fields and rejection rules.
- Never log or expose API keys, image data, or full provider request bodies.
- Keep the ordinary text request prefix stable unless the request explicitly contains images or protocol-specific fields.
- Use existing provider registration, config, and test patterns; do not add an SDK dependency.
- External Agnes smoke tests are diagnostic only and must report server queue failures separately from local test results.

## Tasks

### Task 1: Provider wire regression tests

**Files:**
- Modify: `internal/provider/responses/responses_test.go`
- Modify: `internal/provider/openai/openai_test.go`

- [x] Add a Responses fixture that sends a standard `input_image` part and returns one ordinary JSON response with `output[].content[].text` and usage.
- [x] Add a Responses fixture for the existing SSE event stream and assert both paths produce the same text, usage, reasoning metadata, tool calls, and done chunk.
- [x] Add Chat Completions fixtures for non-stream JSON, SSE, image parts, and a gateway that omits usage.
- [x] Run the focused tests and confirm they fail before production changes.

### Task 2: Implement protocol response normalization

**Files:**
- Modify: `internal/provider/responses/responses.go`
- Modify: `internal/provider/openai/openai.go`

- [x] Normalize a non-stream Responses JSON body into provider chunks while preserving output text, reasoning, function calls, usage aliases, response ID, incomplete status, and provider errors.
- [x] Keep the existing Responses SSE parser and share finalization semantics with the JSON path.
- [x] Allow Chat Completions to parse an ordinary JSON completion and keep forced-SSE tolerance.
- [x] Make `stream_options.include_usage` capability-controlled instead of unconditional for compatible gateways.
- [x] Preserve the existing `image_url` Chat shape and `input_image` Responses shape exactly.

### Task 3: Preserve ModLens v2 at the vision boundary

**Files:**
- Modify: `internal/vision/describer.go`
- Modify: `internal/vision/evidence.go`
- Modify: `internal/vision/describer_test.go`
- Modify: `internal/vision/evidence_test.go`

- [x] Add tests for valid ModLens v2 JSON, missing required fields, wrong field types, oversized output, and fenced/non-JSON output.
- [x] Keep the fixed extractor prompt and image references provider-visible without allowing the model to answer the user task directly.
- [x] Ensure only validated `Evidence` is rendered into the main conversation; invalid output remains an error for bounded retry.
- [x] Preserve output and evidence size limits.

### Task 4: Config and UI protocol coverage

**Files:**
- Modify: `internal/config/config.go` or the smallest existing provider capability file
- Modify: `desktop/settings_app.go` only if runtime kinds/capabilities need exposure
- Modify: `desktop/frontend/src/components/SettingsPanel.tsx` only if the protocol preview or capability label needs a contract update
- Modify: focused config/frontend tests as required

- [x] Represent protocol capabilities without hard-coding Agnes-specific behavior into the generic request path.
- [x] Keep protocol-specific endpoint previews correct for `/chat/completions`, `/responses`, and `/messages`.
- [x] Add configuration tests for vision-capable model declarations and protocol preservation.

### Task 5: Verification and runtime smoke test

**Files:**
- No source changes unless a focused failure requires one.

- [x] Run `gofmt`, focused Go tests, and `go vet` for touched packages; no frontend source changed, so frontend typecheck was not needed.
- [x] Run the desktop build if source changes affect the desktop binary.
- [x] Use the project key without printing it to test `/models`, Chat image input, and Responses text/image inputs separately.
- [x] Record the observed Agnes `/models` success, Responses text success, and image-request timeout as external-service results, not local pass/fail.
- [x] Start the workspace development app with `REASONIX_DEV=1` and verify `desktop/build/bin/reasonix-desktop-dev.exe`; the official installed process remains untouched.
