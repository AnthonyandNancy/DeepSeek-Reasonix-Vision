# Four Protocol Vision Transport Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the Reasonix visual-model path support the official wire formats for OpenAI Chat Completions, OpenAI Responses, Anthropic Messages, and Gemini GenerateContent, while preserving ModLens v2 and exposing normalized response, safe reasoning, and lifecycle progress in the UI.

**Architecture:** Keep `provider.Request` and `provider.Chunk` transport-neutral. Each protocol provider owns its endpoint, authentication, image representation, thinking fields, structured-output fields, streaming parser, and usage mapping. `internal/vision` remains the only ModLens v2 boundary; the desktop UI consumes normalized response/reasoning/progress events and never needs to understand provider JSON.

**Tech Stack:** Go providers and `net/http`, JSON/SSE parsing, existing config and resolver layers, Wails event bridge, React/TypeScript desktop frontend, existing Go and frontend test harnesses.

## Global Constraints

- The four transports are OpenAI Chat Completions, OpenAI Responses, Anthropic Messages, and Gemini GenerateContent.
- Model names are configuration data only; do not add vendor model names to provider code or protocol selection logic.
- The visual provider must resolve from `C:\Users\guojl\AppData\Roaming\reasonix\config.toml`; never read or fall back to MCP `vision-config.json`.
- Preserve ModLens Output Schema v2, including required `summary`, `ocr`, `layout`, `semantics`, and `uncertainty` fields and rejection of `bbox` and `confidence`.
- Preserve local image bytes inside the Reasonix request model; a provider must explicitly convert them to the source format its official API accepts.
- Do not silently convert an image into another protocol's representation. Each selected protocol must emit only its documented image field; transport errors are surfaced directly.
- Never log API keys, full image data URLs, full request bodies, raw provider error bodies, or hidden model reasoning.
- Show provider-returned reasoning summaries or explicit thinking deltas only when the provider exposes them; do not synthesize or display hidden chain-of-thought.
- Keep normal text prompt-prefix bytes stable. Protocol-specific fields may change only on requests that require them.

## Official Wire Matrix

| Protocol | Endpoint and auth | Text and image input | Thinking / structured output | Normalized response |
| --- | --- | --- | --- | --- |
| OpenAI Chat Completions | `POST {base_url}/chat/completions`, `Authorization: Bearer <key>` | `messages[].content` is a string or parts: `{type:"text",text}` and `{type:"image_url",image_url:{url:<data-or-public-url>,detail?}}` | `reasoning_effort` only when the selected model/endpoint documents it; JSON mode uses `response_format:{type:"json_object"}`; `stream` controls SSE | JSON `choices[].message.content` or SSE `choices[].delta.content`; reasoning comes from documented provider reasoning fields; usage from `usage` |
| OpenAI Responses | `POST {base_url}/responses`, `Authorization: Bearer <key>` | `instructions` for system text; `input` string or items with `{role:"user",content:[{type:"input_text",text},{type:"input_image",image_url:<url>,detail?}]}` | `reasoning:{effort}` when supported; structured output uses `text.format`; `max_output_tokens` is the total output budget; omit optional `stream` when false unless an endpoint explicitly requires it | JSON `output[]` message/reasoning items or SSE events such as output-text and reasoning-summary deltas; usage from `usage` aliases |
| Anthropic Messages | `POST {base_url}/messages`, `x-api-key: <key>`, and `anthropic-version: 2023-06-01` | `system` is separate; `messages[].content` parts use `{type:"text",text}` and `{type:"image",source:{type:"base64",media_type,data}}` for local images; URL source is used only when the official endpoint documents it | `thinking:{type:"enabled",budget_tokens:N}` when supported; no generic OpenAI `reasoning_effort`; structured JSON is prompt/parser-validated unless the endpoint documents a native format; streaming uses Messages SSE events | `content[]` blocks: `text`, `thinking`, and `tool_use`; SSE `content_block_delta` maps to text/thinking/tool chunks; usage from `message_start`/`message_delta` |
| Gemini GenerateContent | `POST {base_url}/models/{model}:generateContent` or `streamGenerateContent`, `x-goog-api-key: <key>` | `systemInstruction.parts[].text`; `contents[].parts` use `{text}` plus `{inline_data:{mime_type,data}}` for local images or `{file_data:{mime_type,file_uri}}` when the official API provides a file URI | `generationConfig.responseMimeType:"application/json"` for ModLens JSON; thinking uses the documented `thinkingConfig` for the selected Gemini API version; no OpenAI `reasoning_effort` | `candidates[].content.parts[].text`, thought-marked parts/signatures when exposed, `usageMetadata`; stream chunks are merged by candidate/part |

## File Map

- Modify `internal/provider/provider.go` for transport-neutral image input, normalized reasoning metadata, response metadata, and progress helpers.
- Modify `internal/provider/openai/openai.go` and `internal/provider/openai/openai_test.go` for official Chat Completions fields and JSON/SSE normalization.
- Modify `internal/provider/responses/responses.go`, `internal/provider/responses/json.go`, and their tests for official Responses input/output/event handling and optional-field behavior, with no Chat fallback.
- Modify `internal/provider/anthropic/anthropic.go` and `internal/provider/anthropic/image_test.go` for official Messages image/thinking blocks and SSE mapping.
- Create `internal/provider/gemini/gemini.go`, `internal/provider/gemini/gemini_test.go`, and focused stream fixtures for native GenerateContent.
- Modify `internal/config/config.go`, `internal/config/vision.go`, `internal/config/render.go`, and config tests for protocol capabilities, image-source requirements, and model-only configuration.
- Modify `internal/boot/resolver.go` and `internal/boot/boot.go` only where the new provider factory and capability metadata must be passed through.
- Modify `internal/vision/describer.go`, `internal/vision/evidence.go`, and vision tests to keep the fixed ModLens prompt, attach images through the selected transport, and parse only normalized text.
- Modify `internal/event/event.go`, `internal/eventwire/wire.go`, and event tests for normalized response/reasoning/progress events.
- Modify `desktop/frontend/src/lib/types.ts`, `desktop/frontend/src/lib/useController.ts`, `desktop/frontend/src/components/Transcript.tsx`, `desktop/frontend/src/App.tsx`, locale files, and styles for live response/reasoning/progress rendering.
- Add focused provider, vision, eventwire, and frontend tests before implementation changes.

### Task 1: Freeze The Transport-Neutral Contract

**Files:**
- Modify: `internal/provider/provider.go`
- Test: `internal/provider/provider_test.go`

**Interfaces:**
- Keep `provider.Request.Messages`, `Images`, `Tools`, `MaxTokens`, `Temperature`, `Stream`, and `ResponseFormat` as the caller-facing request model.
- Add normalized metadata to `provider.Chunk` without exposing provider JSON, including response ID, finish reason, usage, and a distinction between visible text and provider-returned reasoning.
- Add a bounded provider progress reporter with closed stages such as `connecting`, `waiting`, `response`, `thinking`, `parsing`, `ready`, and `failed`.

- [ ] **Step 1: Add failing contract tests.** Assert that a normalized stream can carry visible text, reasoning summary, usage, response ID, finish reason, and a terminal error without carrying a request body, key, or image data.
- [ ] **Step 2: Run the focused tests.** Run `go test ./internal/provider`; expected result is failure for the new contract assertions.
- [ ] **Step 3: Implement the smallest neutral types.** Keep protocol-specific names out of the public request model; map each provider into these neutral chunks at its boundary.
- [ ] **Step 4: Verify.** Run `go test ./internal/provider` and `go vet ./internal/provider`; both must pass.

### Task 2: Make OpenAI Chat Completions Official And Complete

**Files:**
- Modify: `internal/provider/openai/openai.go`
- Modify: `internal/provider/openai/openai_test.go`
- Modify: `internal/provider/openai/image_test.go`

**Interfaces:**
- Request URL remains `base_url + "/chat/completions"` unless an explicit full `chat_url` is configured.
- Image parts serialize as the official `image_url.url` object with optional `detail`; no vendor-specific image conversion or alternate endpoint is attempted.
- `Request.ResponseFormat` serializes to the official Chat Completions `response_format` object instead of being silently dropped.

- [ ] **Step 1: Add failing request-shape tests.** Capture a non-stream request and assert exact `model`, `messages`, text part, image part, `stream`, token budget, and `response_format` fields. Assert that no `reasoning_effort` or `thinking` field appears when the endpoint is configured as ordinary chat.
- [ ] **Step 2: Add failing stream tests.** Feed SSE text deltas, provider reasoning deltas, usage, finish reason, and `[DONE]`; assert normalized visible text, reasoning, usage, and terminal metadata.
- [ ] **Step 3: Implement official serialization/parsing.** Send reasoning fields only when the selected Chat protocol configuration explicitly enables the documented field; never infer vendor fields from the model name.
- [ ] **Step 4: Verify.** Run `go test ./internal/provider/openai` and `go vet ./internal/provider/openai`.

### Task 3: Make OpenAI Responses Official And Complete

**Files:**
- Modify: `internal/provider/responses/responses.go`
- Modify: `internal/provider/responses/json.go`
- Modify: `internal/provider/responses/responses_test.go`
- Modify: `internal/provider/responses/json_test.go`

**Interfaces:**
- Build `instructions`, `input`, `max_output_tokens`, `reasoning`, and `text.format` according to the Responses schema.
- Encode images as `input_image` parts with the documented `image_url` value type; do not reuse Chat `image_url` objects inside Responses.
- For non-stream mode, omit `stream` when false by default; send it only when the endpoint explicitly requires the field. For stream mode, parse the official event stream rather than assuming Chat SSE names.

- [ ] **Step 1: Add failing JSON tests.** Cover text input, system instructions, `input_text`, `input_image`, reasoning items, message output items, `incomplete_details`, and usage aliases.
- [ ] **Step 2: Add failing SSE tests.** Cover response creation, output text deltas, reasoning summary deltas, output item completion, usage, response completion, and provider error events.
- [ ] **Step 3: Implement the official Responses request and response paths.** Do not retry a Responses request through Chat Completions; a Responses error remains a Responses error and is surfaced with the selected protocol and status.
- [ ] **Step 4: Verify.** Run `go test ./internal/provider/responses ./internal/provider/openai` and confirm Chat and Responses request bodies are not interchangeable.

### Task 4: Make Anthropic Messages Official And Complete

**Files:**
- Modify: `internal/provider/anthropic/anthropic.go`
- Modify: `internal/provider/anthropic/image_test.go`
- Modify: `internal/provider/anthropic/anthropic_test.go`

**Interfaces:**
- Use `system` outside `messages`; use native authentication/version headers and configured Bearer compatibility only when explicitly enabled.
- Convert a local data URL into an Anthropic base64 image source with `media_type` and base64 `data`; do not send OpenAI `image_url` blocks.
- Map `thinking_delta`, `text_delta`, `input_json_delta`, `message_delta`, and usage events into normalized chunks.

- [ ] **Step 1: Add failing image tests.** Assert exact `source.type`, `source.media_type`, and base64 `source.data`, plus the absence of `image_url`.
- [ ] **Step 2: Add failing thinking/stream tests.** Assert native thinking blocks and deltas are surfaced as normalized reasoning, while tool JSON remains a tool-call delta.
- [ ] **Step 3: Implement official Messages serialization and parsing.** Preserve required `max_tokens` and provider-specific thinking budget validation.
- [ ] **Step 4: Verify.** Run `go test ./internal/provider/anthropic` and `go vet ./internal/provider/anthropic`.

### Task 5: Add Native Gemini GenerateContent

**Files:**
- Create: `internal/provider/gemini/gemini.go`
- Create: `internal/provider/gemini/gemini_test.go`
- Modify: `internal/provider/provider.go` only if registration types need a shared declaration
- Modify: `internal/config/provider_presets.go` only for protocol metadata, never model hardcoding

**Interfaces:**
- Register the provider under a dedicated kind such as `gemini` and keep the existing OpenAI-compatible Gemini route separate from native GenerateContent.
- Serialize `systemInstruction`, `contents`, `parts`, `inline_data`/`file_data`, `generationConfig`, and native auth exactly as configured.
- Parse candidate parts, thought markers/signatures, finish reasons, and `usageMetadata` into normalized chunks.

- [ ] **Step 1: Add failing request tests.** Assert text plus `inline_data` image parts, MIME type preservation, system instruction placement, JSON response MIME type, and native endpoint URL.
- [ ] **Step 2: Add failing stream tests.** Assert text/reasoning part merging, candidate completion, usage metadata, and malformed-event errors.
- [ ] **Step 3: Implement the native transport.** Reject unsupported tool/reasoning combinations before the request and preserve the selected model string verbatim.
- [ ] **Step 4: Verify.** Run `go test ./internal/provider/gemini ./internal/provider` and `go vet ./internal/provider/gemini`.

### Task 6: Add Protocol Capabilities Without Model Hardcoding

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/vision.go`
- Modify: `internal/config/render.go`
- Modify: `internal/boot/resolver.go`
- Modify: `internal/boot/boot.go`
- Test: `internal/config/render_test.go`
- Test: `internal/config/edit_test.go`
- Test: `internal/boot/boot_test.go`

**Interfaces:**
- Add protocol capability values for image field shape (`image_url`, `input_image`, `image.source`, `inline_data`), thinking field shape, structured output field shape, and stream support.
- Keep `models` and `vision_models` as the only model capability lists supplied by configuration; no model IDs are required in source code.
- Preserve `api_key_env` resolution from the Reasonix global `.env` and keep MCP plugin configuration independent.

- [ ] **Step 1: Add failing round-trip tests.** Render and reload each protocol with a generic model name, `vision_models`, image mode, thinking mode, and response format settings; assert no model-name defaults are injected.
- [ ] **Step 2: Add failing validation tests.** Reject protocol/image-field combinations that cannot represent the supplied image, and produce an actionable error before network I/O when the selected official wire format cannot be built.
- [ ] **Step 3: Implement capability-driven resolution.** Pass only the selected protocol's capability metadata into its provider factory.
- [ ] **Step 4: Verify.** Run `go test ./internal/config ./internal/boot` and confirm `vision-config.json` is never loaded by this path.

### Task 7: Preserve ModLens v2 And Add Image Transport Resolution

**Files:**
- Modify: `internal/vision/describer.go`
- Modify: `internal/vision/evidence.go`
- Modify: `internal/vision/toolimages.go`
- Test: `internal/vision/describer_test.go`
- Test: `internal/vision/evidence_test.go`
- Test: `internal/vision/toolimages_test.go`

**Interfaces:**
- Keep the fixed ModLens extractor prompt and `Evidence` parser unchanged as the output contract.
- Use the selected provider's image capability to choose the official wire representation; do not let the describer construct protocol JSON.
- Keep the description in the provider-neutral user message and require JSON output through the protocol-specific structured-output option when supported.

- [ ] **Step 1: Add failing transport tests.** For the same local image and description, assert the four provider request bodies differ only according to their official protocol shape, while the description and ModLens instruction remain equivalent.
- [ ] **Step 2: Add failing failure tests.** Assert malformed or unsupported image data fails quickly with a protocol-specific error; no 180-second blind wait or silent protocol fallback is allowed.
- [ ] **Step 3: Implement transport selection.** Keep `imageDataURLs` as internal storage, but let only the selected protocol serializer convert it to `image_url`, `input_image`, Anthropic `image.source`, or Gemini `inline_data`.
- [ ] **Step 4: Verify ModLens.** Assert final accepted output still parses as ModLens v2 and rejects fenced invalid JSON, missing required fields, `bbox`, and `confidence`.

### Task 8: Show Response, Safe Thinking, And Lifecycle Progress

**Files:**
- Modify: `internal/event/event.go`
- Modify: `internal/eventwire/wire.go`
- Modify: `internal/vision/describer.go`
- Modify: `internal/provider/provider.go`
- Modify: `desktop/frontend/src/lib/types.ts`
- Modify: `desktop/frontend/src/lib/useController.ts`
- Modify: `desktop/frontend/src/components/Transcript.tsx`
- Modify: `desktop/frontend/src/App.tsx`
- Modify: `desktop/frontend/src/locales/zh.ts`
- Modify: `desktop/frontend/src/locales/en.ts`
- Modify: `desktop/frontend/src/locales/zh-TW.ts`
- Modify: `desktop/frontend/src/styles.css`
- Test: `internal/eventwire/wire_test.go`
- Test: `internal/vision/describer_test.go`
- Test: `desktop/frontend/src/__tests__/use-controller-vision-progress.test.ts`
- Test: `desktop/frontend/src/__tests__/vision-progress-card.test.tsx`

**Interfaces:**
- Add normalized event fields for `response_delta`, `reasoning_delta`, `usage`, and `vision_progress`; keep progress records replaceable instead of appending one transcript row per event. Never emit an event for a different protocol.
- Use lifecycle stages `preparing`, `connecting`, `waiting`, `response`, `thinking`, `parsing`, `ready`, `failed`, and `cancelled`.
- Render visible assistant response incrementally, render provider-returned reasoning in a collapsible safe panel, and render elapsed time/model/stage in a compact status card.

- [ ] **Step 1: Add failing event tests.** Assert wire names, field stability, bounded details, and that event payloads contain no raw provider body, key, or image data.
- [ ] **Step 2: Add failing controller tests.** Assert response and reasoning deltas update the active turn, progress stages replace one card, and terminal events release the composer.
- [ ] **Step 3: Implement backend event emission.** Emit `response` when visible text arrives, `thinking` only for explicit provider reasoning chunks, `parsing` before ModLens parsing, and terminal state on completion/failure/cancellation. Protocol errors terminate the selected protocol directly.
- [ ] **Step 4: Implement the UI card and reasoning fold.** Use existing transcript/process styling, accessible `role="status"`, `aria-live="polite"`, keyboard-accessible collapse, and no raw chain-of-thought fallback.
- [ ] **Step 5: Verify.** Run the focused Go event/vision tests and frontend controller/card tests plus `pnpm typecheck`.

### Task 9: Provider Contract Matrix And Real Smoke Tests

**Files:**
- Create: `internal/provider/vision_contract_test.go`
- Create: `internal/provider/testdata/` fixtures for four JSON/SSE response families
- Modify: `docs/VISUAL_EVIDENCE.md`
- Modify: `docs/GUIDE.zh-CN.md`

- [ ] **Step 1: Add a table-driven local contract suite.** For each protocol assert endpoint, auth headers, system placement, text description, image field, thinking field, structured-output field, stream event names, visible response, reasoning, finish state, and usage.
- [ ] **Step 2: Add negative compatibility cases.** Cover wrong-protocol image fields, unsupported reasoning fields, Responses endpoints that require omitted optional fields, and providers that return forced SSE for non-stream requests; none may trigger a different protocol.
- [ ] **Step 3: Add live smoke commands.** Use the selected provider from `C:\Users\guojl\AppData\Roaming\reasonix\config.toml` and its global `.env` key without printing secrets; test text, image, structured ModLens output, streaming, and timeout separately.
- [ ] **Step 4: Verify the product build.** Run `gofmt`, `go vet ./...`, focused provider/vision/event tests, frontend typecheck/build, and start only the development binary with `REASONIX_DEV=1`.

## Acceptance Criteria

- Every protocol emits its own official request shape; no provider receives another protocol's image, thinking, or response-format fields.
- A user can add arbitrary model IDs in `config.toml` and mark vision models without source changes.
- Local images are encoded according to the selected protocol's official field shape; unsupported or malformed input fails explicitly instead of hanging.
- The visual request shows preparation, connection, waiting, response, thinking when explicitly available, parsing, completion, timeout, and failure states.
- The visible assistant response streams incrementally when the provider supports streaming; non-stream responses still render as one normalized response.
- ModLens v2 output validation and evidence rendering remain unchanged in behavior.
- Existing official Reasonix installation and MCP configuration are not read, modified, or restarted by the implementation.
