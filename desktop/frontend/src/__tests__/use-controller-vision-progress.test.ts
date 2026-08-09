import { initialState, isTurnActivityEvent, reducer, staleTurnWatchdogDelay } from "../lib/useController";
import type { WireEvent } from "../lib/types";

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

let state = event(initialState, { kind: "turn_started" });
eq(isTurnActivityEvent("vision_progress"), true, "vision progress keeps the active-turn watchdog alive");
state = event(state, { kind: "vision_progress", visionProgress: { stage: "preparing", modelRef: "vision/model" } });
state = event(state, { kind: "vision_progress", visionProgress: { stage: "response", responseDelta: "{\"summary\":" } });
eq(state.visionProgress?.stage, "response", "vision progress replaces the lifecycle stage");
eq(state.visionProgress?.responseDelta, "{\"summary\":", "response delta is retained");
eq(state.visionProgressHistory?.map((progress) => progress.stage).join(">"), "preparing>response", "vision lifecycle stages are retained in order");
state = event(state, { kind: "vision_progress", visionProgress: { stage: "response", responseDelta: "\"visible\"}" } });
eq(state.visionProgress?.responseDelta, "{\"summary\":\"visible\"}", "response deltas accumulate without appending cards");
eq(state.visionProgressHistory?.[1]?.responseDelta, "{\"summary\":\"visible\"}", "same-stage response deltas merge into one history entry");
state = event(state, { kind: "vision_progress", visionProgress: { stage: "thinking", reasoningDelta: "checking" } });
eq(state.visionProgress?.reasoningDelta, "checking", "reasoning delta is retained separately");
eq(state.visionProgressHistory?.map((progress) => progress.stage).join(">"), "preparing>response>thinking", "stage changes append a new history entry");
state = event(state, { kind: "vision_progress", visionProgress: { stage: "response", responseDelta: "!" } });
eq(state.visionProgressHistory?.map((progress) => progress.stage).join(">"), "preparing>response>thinking", "alternating deltas merge into the existing stage row");
eq(state.visionProgressHistory?.[1]?.responseDelta, "{\"summary\":\"visible\"}!", "alternating response delta is accumulated on its original stage");
state = event(state, { kind: "vision_progress", visionProgress: { stage: "ready", elapsedMs: 42 } });
eq(state.visionProgress?.stage, "ready", "ready stage is retained after completion");
state = reducer(state, { type: "user", text: "next turn", seq: state.seq });
eq(state.visionProgress, undefined, "optimistic user submission clears the previous vision card");
eq(state.visionProgressHistory, undefined, "optimistic user submission clears the previous vision timeline");
state = event(state, { kind: "turn_started" });
eq(state.visionProgress, undefined, "new turn clears the previous vision card");
eq(state.visionProgressHistory, undefined, "new turn clears the previous vision timeline");

eq(staleTurnWatchdogDelay({ running: true, turnActive: true }, 20_000, 30_000), 20_000, "recent vision activity re-arms the watchdog for the remaining interval");
eq(staleTurnWatchdogDelay({ running: true, turnActive: true }, 20_000, 50_000), 0, "watchdog reconciles after the re-armed interval expires");
eq(staleTurnWatchdogDelay({ running: true, turnActive: true }, 20_000, 60_000, 30_000, 50_000), 20_000, "a completed reconciliation probe re-arms the watchdog with backoff");

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
