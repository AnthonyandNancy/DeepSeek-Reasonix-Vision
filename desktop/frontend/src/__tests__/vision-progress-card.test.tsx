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

await act(async () => root.unmount());
dom.window.close();
process.stdout.write("vision progress card: passed\n");
