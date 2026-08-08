import { initialState, reducer } from "../lib/useController";
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
state = event(state, { kind: "vision_progress", visionProgress: { stage: "preparing", modelRef: "vision/model" } });
state = event(state, { kind: "vision_progress", visionProgress: { stage: "response", responseDelta: "{\"summary\":" } });
eq(state.visionProgress?.stage, "response", "vision progress replaces the lifecycle stage");
eq(state.visionProgress?.responseDelta, "{\"summary\":", "response delta is retained");
state = event(state, { kind: "vision_progress", visionProgress: { stage: "response", responseDelta: "\"visible\"}" } });
eq(state.visionProgress?.responseDelta, "{\"summary\":\"visible\"}", "response deltas accumulate without appending cards");
state = event(state, { kind: "vision_progress", visionProgress: { stage: "thinking", reasoningDelta: "checking" } });
eq(state.visionProgress?.reasoningDelta, "checking", "reasoning delta is retained separately");
state = event(state, { kind: "vision_progress", visionProgress: { stage: "ready", elapsedMs: 42 } });
eq(state.visionProgress?.stage, "ready", "ready stage is retained after completion");
state = event(state, { kind: "turn_started" });
eq(state.visionProgress, undefined, "new turn clears the previous vision card");

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
