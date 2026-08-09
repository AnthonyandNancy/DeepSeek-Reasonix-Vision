# Vision Timeline Anchoring Design

## Problem

The desktop transcript stores live activity in one flat `Item[]`. On
`turn_started`, the reducer inserts an empty assistant item before any visual
analysis begins. A later `vision_progress` event appends a visual item, while
the main model's eventual reasoning and answer are written back into the older
assistant item. The rendered order therefore becomes assistant then vision,
even when the visual analysis happened first.

History replay already places durable visual records immediately after their
owning user or tool message. Live and replayed transcripts consequently disagree.
The live wire contract also carries no owner for a visual analysis, so a visual
bridge for one tool can appear after unrelated parallel tools.

## Required Ordering

- Host attachment analysis: user, visual analysis, main-model reasoning/answer.
- Explicit main-model visual tool: main-model reasoning, tool call, visual
  analysis, subsequent main-model reasoning/answer.
- Tool-media bridge: source tool, its visual analysis, then unrelated later
  tools or model output.
- Retries and terminal updates replace the same visual item without moving it.
- Live, tab-cached, refreshed, and replayed transcripts have identical order.

## Architecture

### Visual ownership contract

`VisionProgressInfo` gains local-only `OwnerKind` and `OwnerID` fields.
`OwnerKind` is `user` or `tool`. A user-owned event may omit `OwnerID` because
the frontend owns the active user item's local ID. A tool-owned event carries
the exact provider tool-call ID.

The fields are transient UI metadata. They never enter provider requests,
ModLens evidence, model prompts, or MCP routing.

### Tool invocation identity

The boot layer injects an invocation-ID resolver into the built-in
`analyze_media_with_vision` tool. That resolver reads the existing
`agent.CallContext` without coupling the vision package back to the agent. The
tool-image bridge uses the `ToolImageInput.ToolCallID` already supplied by the
agent.

### Frontend timeline insertion

The empty assistant created by `turn_started` remains visible as a loading
cursor, but it is treated as a movable placeholder until it receives text or
reasoning. New tool cards that arrive before model content are inserted before
that placeholder.

The first event for a visual analysis inserts its item after its owner:

- `user`: after the latest active user and existing user-owned visual siblings;
- `tool`: after the matching tool and existing visual children for that tool.

If a tool-owned visual event arrives before its tool card, it is inserted before
the empty assistant and re-anchored when the matching tool card arrives.
Subsequent events merge by `analysisId` and keep the established location.

### History replay

History continues to infer ownership from the message that stores each durable
visual record. Replayed visual items receive the same frontend owner fields as
live items, keeping future rendering and updates consistent.

## Compatibility

Older wire events without owner fields use the initiator as a fallback:
`host_auto` is user-owned; other visual initiators retain event order and are
placed before an untouched assistant placeholder. Unknown ownership never
causes an existing visual item to jump to the session tail.

## Verification

Tests must drive the reducer with real event order rather than hand-constructing
an already-correct `Item[]`. Coverage includes first-turn images, no-reasoning
tool calls, parallel tools, multi-image analysis, retries, cancellation, history
replay, and DOM order in both transcript display modes.
