# Provider Request URL Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the custom provider request URL preview and full-URL auto-fill follow the selected connection protocol.

**Architecture:** Keep URL derivation in the existing frontend helper. Add the selected provider kind as an input, map each supported transport to its endpoint suffix, and preserve explicit full URLs unchanged. The backend provider transport is outside this change because it already receives and uses the stored kind.

**Tech Stack:** React, TypeScript, Vitest-style frontend tests.

## Global Constraints

- Keep manual full URL editing intact.
- Preserve the existing default behavior for callers that do not pass a protocol.
- Do not modify provider request serialization or configuration persistence.
- Use `apply_patch` for source edits and run focused frontend tests plus the production build.

---

### Task 1: Protocol-aware request URL mapping

**Files:**
- Modify: `desktop/frontend/src/components/SettingsPanel.tsx:885-889,6207,6544-6545`
- Test: `desktop/frontend/src/__tests__/settings-refresh-snapshot.test.tsx:173-176`

**Interfaces:**
- `providerChatURLPreview(baseUrl, chatUrl, fullURL, kind?)` returns a trimmed explicit URL when `fullURL` is true; otherwise it appends `/chat/completions` for `openai`, `/responses` for `responses`, `/messages` for `anthropic`, and defaults to `/chat/completions` for unknown or omitted kinds.

- [ ] **Step 1: Add failing assertions for all protocol suffixes**

  Extend the existing helper assertions with:

  ```ts
  eq(providerChatURLPreview("https://proxy.example.com/v1", "", false, "responses"), "https://proxy.example.com/v1/responses", "Responses preview uses the Responses endpoint");
  eq(providerChatURLPreview("https://proxy.example.com/v1", "", false, "anthropic"), "https://proxy.example.com/v1/messages", "Anthropic preview uses the Messages endpoint");
  eq(providerChatURLPreview("https://proxy.example.com/v1", "", false, "openai"), "https://proxy.example.com/v1/chat/completions", "OpenAI preview uses the Chat Completions endpoint");
  eq(providerChatURLPreview("https://proxy.example.com/v1", "", false, "unknown"), "https://proxy.example.com/v1/chat/completions", "Unknown protocols keep the compatible default");
  ```

- [ ] **Step 2: Run the focused test and verify the new assertions fail**

  Run from `desktop/frontend`:

  ```powershell
  pnpm vitest run src/__tests__/settings-refresh-snapshot.test.tsx
  ```

  Expected: the existing two-argument assertions pass, and the new protocol-specific assertions fail because the helper currently always appends `/chat/completions`.

- [ ] **Step 3: Implement protocol-aware URL derivation**

  Update the helper signature and mapping:

  ```ts
  export function providerChatURLPreview(baseUrl: string, chatUrl: string, fullURL: boolean, kind = "openai"): string {
    if (fullURL) return trimmedURL(chatUrl);
    const base = trimmedURL(baseUrl);
    if (!base) return "";
    const suffix = kind.trim().toLowerCase() === "responses"
      ? "/responses"
      : kind.trim().toLowerCase() === "anthropic"
        ? "/messages"
        : "/chat/completions";
    return `${base}${suffix}`;
  }
  ```

  Pass `effectiveKind` at the preview call and when initializing `chatUrl` after enabling full URL:

  ```tsx
  const previewChatUrl = providerChatURLPreview(baseUrl, chatUrl, fullChatUrl, effectiveKind);
  setChatUrl(providerChatURLPreview(baseUrl, "", false, effectiveKind));
  ```

- [ ] **Step 4: Run the focused test and verify all URL cases pass**

  Run:

  ```powershell
  pnpm vitest run src/__tests__/settings-refresh-snapshot.test.tsx
  ```

  Expected: PASS, including the existing full-URL preservation and base-URL normalization assertions.

- [ ] **Step 5: Run the frontend build**

  Run:

  ```powershell
  pnpm build
  ```

  Expected: the frontend production build completes successfully.
