import { JSDOM } from "jsdom";
import React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { VisionProgressCard } from "../components/Transcript";
import { LocaleProvider } from "../lib/i18n";
import type { WireVisionProgress } from "../lib/types";

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", { pretendToBeVisual: true, url: "http://localhost/" });
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  Node: dom.window.Node,
  Element: dom.window.Element,
  HTMLElement: dom.window.HTMLElement,
});

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("missing root");
const root = createRoot(rootEl);

const progress: WireVisionProgress = {
  stage: "response",
  modelRef: "vision/model",
  responseDelta: "{\"summary\":\"visible\"}",
  reasoningDelta: "checking pixels",
  elapsedMs: 1250,
};
await act(async () => {
  root.render(React.createElement(LocaleProvider, null, React.createElement(VisionProgressCard, { progress })));
});

if (!document.querySelector('[role="status"]')) throw new Error("vision card must expose status semantics");
if (!document.body.textContent?.includes("vision/model")) throw new Error("vision card must show model");
if (!document.body.textContent?.includes("visible")) throw new Error("vision card must show response output");
if (!document.body.textContent?.includes("checking pixels")) throw new Error("vision card must show safe reasoning output");
if (document.querySelectorAll("details[open]").length < 2) throw new Error("active vision reasoning and response sections must be open");

await act(async () => {
  root.render(React.createElement(LocaleProvider, null, React.createElement(VisionProgressCard, {
    history: [
      { stage: "preparing", modelRef: "vision/model" },
      { stage: "connecting", modelRef: "vision/model" },
      { stage: "waiting", modelRef: "vision/model" },
    ],
  })));
});
if (document.querySelectorAll(".vision-progress__detail").length !== 3) throw new Error("every vision lifecycle stage must explain its current work");
const lifecycleCopy = document.body.textContent ?? "";
const activeCopyCount = (lifecycleCopy.match(/In progress|进行中|進行中/g) ?? []).length;
if (activeCopyCount > 1) throw new Error("historical vision stages must not remain labeled as active");

await act(async () => {
  root.render(React.createElement(LocaleProvider, null, React.createElement(VisionProgressCard, {
    history: [{ stage: "cancelled", detail: "authentication_failed" }],
  })));
});
const cancelledCopy = document.body.textContent ?? "";
if (!cancelledCopy.includes("Cancelled") && !cancelledCopy.includes("已取消")) throw new Error("cancelled vision stage must use the localized state label");
if (document.body.textContent?.includes("authentication_failed")) throw new Error("raw provider detail enums must not leak into localized progress copy");

await act(async () => root.unmount());
dom.window.close();
process.stdout.write("vision progress card: passed\n");
