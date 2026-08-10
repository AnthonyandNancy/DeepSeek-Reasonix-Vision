import { historyMessagesToItems, initialState, isTurnActivityEvent, reducer, staleTurnWatchdogDelay } from "../lib/useController";
import type { HistoryMessage, WireEvent } from "../lib/types";

let passed = 0;
let failed = 0;

function eq(a: unknown, b: unknown, label: string) {
  if (a === b) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(b)}, got ${JSON.stringify(a)}\n`);
    failed += 1;
  }
}

function event(s: typeof initialState, e: WireEvent) {
  return reducer(s, { type: "event", e });
}

function visionEvent(visionProgress: Record<string, unknown>): WireEvent {
  return { kind: "vision_progress", visionProgress } as unknown as WireEvent;
}

function visionItems(state: typeof initialState) {
  return state.items.filter((item) => item.kind === "vision") as Array<{
    analysisId: string;
    analysis: {
      status: string;
      media_count?: number;
      elapsed_ms?: number;
      stages: Array<{ attempt?: number; stage: string; response?: string; reasoning?: string; duration_ms?: number }>;
    };
  }>;
}

{
  let stable = reducer(initialState, { type: "user", text: "retry this image", seq: initialState.seq });
  stable = event(stable, { kind: "turn_started" });
  stable = event(stable, visionEvent({ analysisId: "stable-vision", initiator: "host_auto", ownerKind: "user", attempt: 1, mediaCount: 3, stage: "preparing" }));
  stable = event(stable, visionEvent({ analysisId: "stable-vision", attempt: 1, stage: "failed", detail: "timeout" }));
  stable = event(stable, visionEvent({ analysisId: "stable-vision", attempt: 2, stage: "preparing" }));
  stable = event(stable, visionEvent({ analysisId: "stable-vision", attempt: 2, stage: "cancelled" }));
  eq(itemOrder(stable), "user>vision:stable-vision>assistant", "retry and cancellation updates keep the visual item at its original anchor");
  eq(visionItems(stable).length, 1, "multi-image retries keep one visual transcript item");
  eq(visionItems(stable)[0]?.analysis.media_count, 3, "multi-image ownership retains the media count");
}

function itemOrder(state: typeof initialState): string {
  return state.items.map((item) => {
    if (item.kind === "tool") return `tool:${item.id}`;
    if (item.kind === "vision") return `vision:${item.analysisId}`;
    return item.kind;
  }).join(">");
}

{
  let live = reducer(initialState, { type: "user", text: "inspect this image", seq: initialState.seq });
  live = event(live, { kind: "turn_started" });
  live = event(live, visionEvent({
    analysisId: "host-live",
    initiator: "host_auto",
    ownerKind: "user",
    attempt: 1,
    mediaCount: 1,
    stage: "preparing",
  }));
  live = event(live, { kind: "reasoning", reasoning: "read the visual evidence" });
  live = event(live, { kind: "message", text: "the image shows a dialog", reasoning: "read the visual evidence" });
  eq(itemOrder(live), "user>vision:host-live>assistant", "host-auto analysis stays before the assistant placeholder it precedes");
}

{
  let direct = reducer(initialState, { type: "user", text: "reanalyze", seq: initialState.seq });
  direct = event(direct, { kind: "turn_started" });
  direct = event(direct, { kind: "tool_dispatch", tool: { id: "call-v", name: "analyze_media_with_vision", args: "{}", readOnly: true } });
  direct = event(direct, visionEvent({
    analysisId: "tool-live",
    initiator: "main_model_tool",
    ownerKind: "tool",
    ownerId: "call-v",
    attempt: 1,
    stage: "preparing",
  }));
  direct = event(direct, { kind: "tool_result", tool: { id: "call-v", name: "analyze_media_with_vision", output: "evidence", readOnly: true } });
  direct = event(direct, { kind: "text", text: "new answer" });
  direct = event(direct, { kind: "message", text: "new answer" });
  eq(itemOrder(direct), "user>tool:call-v>vision:tool-live>assistant", "tool-first visual analysis stays before a later assistant answer");
}

{
  let parallel = reducer(initialState, { type: "user", text: "use both tools", seq: initialState.seq });
  parallel = event(parallel, { kind: "turn_started" });
  parallel = event(parallel, { kind: "reasoning", reasoning: "call two tools" });
  parallel = event(parallel, { kind: "message", reasoning: "call two tools" });
  parallel = event(parallel, { kind: "tool_dispatch", tool: { id: "image-tool", name: "capture", args: "{}", readOnly: true } });
  parallel = event(parallel, { kind: "tool_dispatch", tool: { id: "text-tool", name: "read_file", args: "{}", readOnly: true } });
  parallel = event(parallel, visionEvent({
    analysisId: "bridge-live",
    initiator: "tool_media_bridge",
    ownerKind: "tool",
    ownerId: "image-tool",
    attempt: 1,
    stage: "preparing",
  }));
  eq(itemOrder(parallel), "user>assistant>tool:image-tool>vision:bridge-live>tool:text-tool", "tool-media analysis follows its exact source tool among parallel tools");
}

{
  let reordered = reducer(initialState, { type: "user", text: "wait for the tool", seq: initialState.seq });
  reordered = event(reordered, { kind: "turn_started" });
  reordered = event(reordered, visionEvent({
    analysisId: "early-vision",
    initiator: "main_model_tool",
    ownerKind: "tool",
    ownerId: "late-tool",
    attempt: 1,
    stage: "preparing",
  }));
  reordered = event(reordered, { kind: "tool_dispatch", tool: { id: "late-tool", name: "analyze_media_with_vision", args: "{}", readOnly: true } });
  eq(itemOrder(reordered), "user>tool:late-tool>vision:early-vision>assistant", "visual progress reanchors when its source tool arrives later");
}

let state = reducer(initialState, { type: "user", text: "compare these images", seq: initialState.seq });
eq(isTurnActivityEvent("vision_progress"), true, "vision progress keeps the active-turn watchdog alive");
state = event(state, visionEvent({ analysisId: "vision-1", initiator: "host_auto", attempt: 1, mediaCount: 2, stage: "preparing", modelRef: "vision/model" }));
eq(visionItems(state).length, 1, "first analysis creates one transcript vision item");
state = event(state, visionEvent({ analysisId: "vision-1", initiator: "host_auto", attempt: 1, mediaCount: 2, stage: "response", responseDelta: "{\"summary\":" }));
state = event(state, visionEvent({ analysisId: "vision-1", attempt: 1, stage: "response", responseDelta: "\"visible\"}" }));
eq(visionItems(state).length, 1, "repeated deltas update the existing vision item");
eq(visionItems(state)[0]?.analysis.stages.find((stage) => stage.stage === "response")?.response, "{\"summary\":\"visible\"}", "response deltas merge into one stage");
state = event(state, visionEvent({ analysisId: "vision-1", attempt: 1, stage: "thinking", reasoningDelta: "checking" }));
state = event(state, visionEvent({ analysisId: "vision-1", attempt: 1, stage: "response", responseDelta: "!" }));
eq(visionItems(state)[0]?.analysis.stages.map((stage) => stage.stage).join(">"), "preparing>response>thinking", "alternating deltas preserve stage order");
eq(visionItems(state)[0]?.analysis.stages.find((stage) => stage.stage === "response")?.response, "{\"summary\":\"visible\"}!", "alternating response delta returns to its stage");
state = event(state, visionEvent({ analysisId: "vision-1", attempt: 2, stage: "preparing" }));
eq(visionItems(state)[0]?.analysis.stages.map((stage) => `${stage.attempt}:${stage.stage}`).join(">"), "1:preparing>1:response>1:thinking>2:preparing", "retry attempts retain their own lifecycle rows");
state = event(state, visionEvent({ analysisId: "vision-2", initiator: "main_model_tool", attempt: 1, mediaCount: 1, stage: "preparing", modelRef: "vision/other" }));
eq(visionItems(state).length, 2, "a second analysis id creates a second transcript item");
state = event(state, visionEvent({ analysisId: "vision-1", attempt: 2, stage: "ready", elapsedMs: 42 }));
eq(visionItems(state)[0]?.analysis.status, "ready", "terminal status stays on its analysis item");
state = reducer(state, { type: "user", text: "next turn", seq: state.seq });
eq(visionItems(state).some((item) => item.analysisId === "vision-1"), true, "optimistic user submission preserves completed visual analysis");
state = event(state, { kind: "turn_started" });
eq(visionItems(state).some((item) => item.analysisId === "vision-1"), true, "turn_started preserves prior visual analysis");

const hydrated = historyMessagesToItems([
  {
    role: "user",
    content: "inspect",
    visualAnalyses: [{
      id: "history-user-vision",
      initiator: "host_auto",
      status: "ready",
      model_ref: "vision/history",
      media_count: 1,
      stages: [{ attempt: 1, stage: "ready", response: "user evidence", duration_ms: 50 }],
      summary: "dialog",
    }],
  },
  {
    role: "assistant",
    content: "",
    toolCalls: [{ id: "call-1", name: "analyze_media_with_vision", arguments: "{}" }],
  },
  {
    role: "tool",
    content: "evidence",
    toolCallId: "call-1",
    toolName: "analyze_media_with_vision",
    visualAnalyses: [{
      id: "history-tool-vision",
      initiator: "main_model_tool",
      status: "ready",
      stages: [{ attempt: 1, stage: "thinking", reasoning: "checked labels" }],
      ocr_text: "Save",
    }],
  },
] as unknown as HistoryMessage[], "h").items;
eq(hydrated.map((item) => item.kind).join(">"), "user>vision>tool>vision", "history restores visual items immediately after their user or tool owner");
eq((hydrated[1] as { analysis?: { summary?: string } }).analysis?.summary, "dialog", "history restores visual summary");
eq((hydrated[1] as { analysis?: { stages?: Array<{ duration_ms?: number }> } }).analysis?.stages?.[0]?.duration_ms, 50, "history restores visual stage durations");
eq((hydrated[3] as { analysis?: { ocr_text?: string } } | undefined)?.analysis?.ocr_text, "Save", "history restores tool visual OCR");
eq(hydrated[1]?.kind === "vision" ? `${hydrated[1].ownerKind}:${hydrated[1].ownerId}` : "", `user:${hydrated[0]?.id}`, "history records the owning user anchor");
eq(hydrated[3]?.kind === "vision" ? `${hydrated[3].ownerKind}:${hydrated[3].ownerId}` : "", "tool:call-1", "history records the owning tool anchor");

let timed = reducer(initialState, { type: "user", text: "time each visual stage", seq: initialState.seq });
timed = event(timed, visionEvent({ analysisId: "timed-vision", attempt: 1, stage: "preparing", elapsedMs: 100, stageElapsedMs: 0 }));
timed = event(timed, visionEvent({ analysisId: "timed-vision", attempt: 1, stage: "connecting", elapsedMs: 150, stageElapsedMs: 0, completedStage: "preparing", completedStageAttempt: 1, completedStageElapsedMs: 150 }));
timed = event(timed, visionEvent({ analysisId: "timed-vision", attempt: 1, stage: "waiting", elapsedMs: 2150, stageElapsedMs: 0, completedStage: "connecting", completedStageAttempt: 1, completedStageElapsedMs: 2000 }));
timed = event(timed, visionEvent({ analysisId: "timed-vision", attempt: 1, stage: "response", elapsedMs: 3150, stageElapsedMs: 0, completedStage: "waiting", completedStageAttempt: 1, completedStageElapsedMs: 1000 }));
timed = event(timed, visionEvent({ analysisId: "timed-vision", attempt: 1, stage: "response", elapsedMs: 5150, stageElapsedMs: 2000 }));
timed = event(timed, visionEvent({ analysisId: "timed-vision", attempt: 1, stage: "thinking", elapsedMs: 6150, stageElapsedMs: 0, completedStage: "response", completedStageAttempt: 1, completedStageElapsedMs: 3000 }));
timed = event(timed, visionEvent({ analysisId: "timed-vision", attempt: 1, stage: "response", elapsedMs: 7150, stageElapsedMs: 3000, completedStage: "thinking", completedStageAttempt: 1, completedStageElapsedMs: 1000 }));
timed = event(timed, visionEvent({ analysisId: "timed-vision", attempt: 1, stage: "parsing", elapsedMs: 8150, stageElapsedMs: 0, completedStage: "response", completedStageAttempt: 1, completedStageElapsedMs: 4000 }));
timed = event(timed, visionEvent({ analysisId: "timed-vision", attempt: 1, stage: "ready", elapsedMs: 8200, stageElapsedMs: 0, completedStage: "parsing", completedStageAttempt: 1, completedStageElapsedMs: 50 }));
eq(visionItems(timed)[0]?.analysis.elapsed_ms, 8200, "visual analysis keeps the total elapsed duration");
eq(visionItems(timed)[0]?.analysis.stages.map((stage) => stage.duration_ms ?? 0).join(","), "150,2000,1000,4000,1000,50,0", "visual stages retain their own durations");

eq(staleTurnWatchdogDelay({ running: true, turnActive: true }, 20_000, 30_000), 20_000, "recent vision activity re-arms the watchdog for the remaining interval");
eq(staleTurnWatchdogDelay({ running: true, turnActive: true }, 20_000, 50_000), 0, "watchdog reconciles after the re-armed interval expires");
eq(staleTurnWatchdogDelay({ running: true, turnActive: true }, 20_000, 60_000, 30_000, 50_000), 20_000, "a completed reconciliation probe re-arms the watchdog with backoff");

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
