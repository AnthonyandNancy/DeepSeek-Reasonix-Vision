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
      stages: Array<{ attempt?: number; stage: string; response?: string; reasoning?: string }>;
    };
  }>;
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
      stages: [{ attempt: 1, stage: "ready", response: "user evidence" }],
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
eq((hydrated[3] as { analysis?: { ocr_text?: string } } | undefined)?.analysis?.ocr_text, "Save", "history restores tool visual OCR");

eq(staleTurnWatchdogDelay({ running: true, turnActive: true }, 20_000, 30_000), 20_000, "recent vision activity re-arms the watchdog for the remaining interval");
eq(staleTurnWatchdogDelay({ running: true, turnActive: true }, 20_000, 50_000), 0, "watchdog reconciles after the re-armed interval expires");
eq(staleTurnWatchdogDelay({ running: true, turnActive: true }, 20_000, 60_000, 30_000, 50_000), 20_000, "a completed reconciliation probe re-arms the watchdog with backoff");

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
