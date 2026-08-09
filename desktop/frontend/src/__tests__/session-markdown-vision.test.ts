import { createServer, type ViteDevServer } from "vite";
import type { Item } from "../lib/useController";

let server: ViteDevServer | undefined;
try {
  server = await createServer({ appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  const { sessionItemsToMarkdown } = await server.ssrLoadModule("/src/App.tsx");
  if (typeof sessionItemsToMarkdown !== "function") throw new Error("session markdown helper must be testable");

  const items: Item[] = [{
    kind: "vision",
    id: "vision:export",
    analysisId: "export",
    analysis: {
      id: "export",
      initiator: "main_model_tool",
      model_ref: "vision/model",
      status: "ready",
      media_count: 1,
      summary: "settings dialog",
      ocr_text: "Save",
      stages: [
        { attempt: 1, stage: "response", response: "structured response" },
        { attempt: 1, stage: "thinking", reasoning: "checking pixels", detail: "authentication_failed" },
      ],
    },
  }];

  const markdown = sessionItemsToMarkdown("Vision export", items);
  for (const expected of ["Visual analysis", "main_model_tool", "vision/model", "settings dialog", "Save", "structured response", "checking pixels"]) {
    if (!markdown.includes(expected)) throw new Error(`session markdown omitted visual content: ${expected}`);
  }
  if (markdown.includes("authentication_failed")) throw new Error("session markdown must not expose raw visual detail enums");
} finally {
  await server?.close();
}

process.stdout.write("session markdown vision: passed\n");
