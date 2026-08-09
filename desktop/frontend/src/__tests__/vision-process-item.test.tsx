import { JSDOM } from "jsdom";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { createServer, type ViteDevServer } from "vite";
import type { Item } from "../lib/useController";

const activeItem: Extract<Item, { kind: "vision" }> = {
  kind: "vision",
  id: "vision:active",
  analysisId: "active",
  analysis: {
    id: "active",
    initiator: "main_model_tool",
    model_ref: "vision/model",
    status: "thinking",
    media_count: 2,
    summary: "settings dialog",
    ocr_text: "Save",
    stages: [
      { attempt: 1, stage: "preparing" },
      { attempt: 1, stage: "response", response: "{\"summary\":\"settings dialog\"}" },
      { attempt: 1, stage: "thinking", reasoning: "checking pixels", elapsed_ms: 1250 },
    ],
  },
};

const noReasoningItem: Extract<Item, { kind: "vision" }> = {
  kind: "vision",
  id: "vision:complete",
  analysisId: "complete",
  analysis: {
    id: "complete",
    initiator: "host_auto",
    status: "ready",
    stages: [{ attempt: 1, stage: "ready", detail: "authentication_failed" }],
  },
};

let server: ViteDevServer | undefined;
try {
  server = await createServer({ appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  const { VisionProcessItem } = await server.ssrLoadModule("/src/components/Transcript.tsx");
  const { LocaleProvider } = await server.ssrLoadModule("/src/lib/i18n.tsx");
  const render = (item: Extract<Item, { kind: "vision" }>) => new JSDOM(renderToStaticMarkup(
    React.createElement(LocaleProvider, null, React.createElement(VisionProcessItem, { item })),
  )).window.document;

  const active = render(activeItem);
  if (!active.querySelector('[role="status"]')) throw new Error("vision process must expose status semantics");
  if (!active.body.textContent?.includes("Main model requested visual analysis")) throw new Error("vision process must localize the initiator");
  if (!active.body.textContent?.includes("vision/model")) throw new Error("vision process must show the configured model");
  if (!active.body.textContent?.includes("settings dialog")) throw new Error("vision process must show the summary");
  if (!active.body.textContent?.includes("Save")) throw new Error("vision process must show OCR text");
  if (!active.body.textContent?.includes("checking pixels")) throw new Error("vision process must show emitted reasoning");
  if (!active.body.textContent?.includes("{\"summary\":\"settings dialog\"}")) throw new Error("vision process must show emitted response content");
  if (!active.querySelector('.turn-collapse__reasoning-head[data-running]')) throw new Error("active vision stage must reuse the native shimmer head");

  const complete = render(noReasoningItem);
  if (complete.querySelector('[data-vision-content="reasoning"]')) throw new Error("vision process must not fabricate reasoning content");
  if (complete.body.textContent?.includes("authentication_failed")) throw new Error("raw provider detail enums must not leak into localized progress copy");
} finally {
  await server?.close();
}

process.stdout.write("vision process item: passed\n");
