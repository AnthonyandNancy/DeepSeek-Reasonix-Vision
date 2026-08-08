# Merge Notes — Reasonix + SupportVisionModel + ModLens v2

Date: 2026-08-07

## Baseline

This tree was built from the uploaded `DeepSeek-Reasonix-main-v2` source ZIP as
the authoritative base. The ZIP has no `.git` metadata, so an exact upstream
`main-v2` commit SHA cannot be proven from the archive itself.

Baseline signals present in the uploaded tree:

- `release-notes/releases.json` includes Reasonix `1.21.0` dated `2026-08-06`;
- `CHANGELOG.md` also contains an `Unreleased` section (including Issue #7575
  and `[ui].show_turn_usage`), so the uploaded snapshot contains source beyond
  the last changelog section shown as a numbered stable release.

The older uploaded `Junjie88/Reasonix-SupportVisionModel` tree was used as a
behavior/reference source. It was **not** merged wholesale over the new tree.
That choice is deliberate: the current Reasonix base already contains newer
provider metadata, native image transport, extension/runtime work, Desktop
changes, and other code that would be regressed by copying old fork files.

## Ported visual-fallback behavior

The synchronization preserves/ports the important behavior of the Junjie fork
onto the newer Reasonix architecture:

- optional `[agent].vision_model` configuration;
- Desktop settings support for selecting the visual fallback model;
- user image attachments are accepted when either the main model is natively
  multimodal or a visual fallback is configured;
- direct native image input remains unchanged for image-capable models;
- text-only main models can automatically route user images through the
  configured visual model before the main inference call;
- image-bearing tool results can be routed through the same visual model for
  text-only agents/sub-agents;
- task/review/research/explore-style child agent construction receives the
  shared tool-image processor;
- raw user image attachments are not blindly inherited by child sessions;
- direct-image two-model flows do not send raw images to an unrelated
  text-only planner;
- visual usage is labeled separately as `vision` usage;
- visual extraction has bounded retries and fail-closed behavior.

## ModLens v2 upgrade

The Junjie fork's free-form image-description handoff has been replaced by a
strict ModLens v2 evidence bridge.

New core package:

- `internal/vision/evidence.go` — ModLens v2 result types, strict parser, host
  evidence renderer, output bounds, boundary neutralization;
- `internal/vision/describer.go` — tool-less auxiliary visual extraction request;
- `internal/vision/toolimages.go` — image-bearing tool-result fallback and
  bounded evidence handoff.

The auxiliary visual model must produce:

- `summary`;
- `ocr.full_text` and `ocr.lines`;
- `layout.regions` with ModLens v2 region types and reading order;
- `semantics.scene`, optional intent, entities and relations;
- optional `visual` clues;
- `uncertainty`.

`bbox` and numeric `confidence` are rejected, matching the current ModLens v2
contract. The main model receives separately labeled DIRECT_EVIDENCE,
SEMANTIC_INTERPRETATION and UNCERTAINTY sections instead of an undifferentiated
natural-language description.

## Hallucination-control choices

The bridge is intentionally conservative:

- OCR/layout/direct visual notes are presented as visual evidence;
- summaries/entities/relations/intent are explicitly presented as semantic
  interpretation;
- uncertainty cannot be promoted into a fact;
- image text is untrusted data and cannot instruct the visual model/main model;
- the auxiliary visual model receives no tools;
- framework/code/root-cause claims must be verified by Reasonix repository/tool
  evidence;
- a malformed visual response is rejected and retried rather than silently
  injected;
- after the bounded attempts fail, Reasonix tells the text model that it could
  not read the image instead of inventing a description.

## Files/areas intentionally based on latest Reasonix

The current Reasonix implementation remains authoritative for:

- provider `vision_models` metadata and `EffectiveVision` capability checks;
- native OpenAI/Responses/Anthropic/etc. provider transports;
- current `provider.Message.Images` image transport;
- current attachment compression and reference resolution;
- current Extension Protocol / plugin runtime;
- current Desktop app/bridge/settings architecture;
- current Agent/Coordinator/Task/Controller implementations.

The old fork was adapted to these APIs rather than replacing them.

## Configuration example

```toml
default_model = "deepseek/deepseek-v4-flash"

[agent]
vision_model = "qwen/qwen3.7-plus"

[[providers]]
name = "qwen"
kind = "openai"
base_url = "https://dashscope.aliyuncs.com/compatible-mode/v1"
api_key_env = "DASHSCOPE_API_KEY"
models = ["qwen3.7-plus"]
vision_models = ["qwen3.7-plus"]
```

See `docs/VISUAL_EVIDENCE.md` for behavior and trust-boundary details.

## Verification status

The package was checked in the supplied sandbox with the following limitations:

- the repository requires `go 1.25.0` with `toolchain go1.26.5`;
- the sandbox's installed Go is older and outbound DNS/network access is blocked;
- therefore the required Go toolchain and missing module dependencies cannot be
  downloaded in this environment, so a full repository `go test ./...` / `go
  vet ./...` cannot complete here.

Before release/upload, run on a normal networked development machine:

```sh
go test ./...
go vet ./...
```

Additional checks run for this packaged tree are recorded/updated at packaging
time below.

### Packaging-time checks

- `gofmt` on all 37 changed/new Go files: completed.
- `git diff --check`: clean.
- isolated ModLens evidence parser/renderer Go tests: `ok evidencecheck` (includes strict v2 shape, bbox/confidence rejection, uncertainty preservation, host-boundary neutralization, bounded wrapper output, and preservation of optional visual fields).
- isolated provider retry-budget regression: `ok retrycheck` (zero transport retries means exactly one HTTP attempt for each outer visual extraction attempt).
- changed TypeScript/TSX syntax parse: 7 files parsed, 0 syntax diagnostics. This is syntax validation only; frontend dependencies were not present for a complete project typecheck/build.
- `reasonix.example.toml`: parsed successfully with Python `tomllib`.
- stale old free-form `vision-description` implementation scan: none found in `internal` or `desktop`.
- full `go test ./...`: **not executed to compilation** because Go attempted to download required toolchain `go1.26.5` and sandbox DNS/network access to `proxy.golang.org` was refused.
- full `go vet ./...`: same `go1.26.5` download/network blocker.

Observed full-suite blocker (environment, before repository compilation):

```text
go: downloading go1.26.5 (linux/amd64)
go: download go1.26.5: golang.org/toolchain@v0.0.1-go1.26.5.linux-amd64:
Get "https://proxy.golang.org/golang.org/toolchain/@v/v0.0.1-go1.26.5.linux-amd64.zip":
dial tcp: lookup proxy.golang.org ... connection refused / i/o timeout
```

Because the full repository could not reach its required toolchain, this package must be treated as **source-synchronized and statically/isolated-verified, but not full-suite compiled in this sandbox**. Run `go test ./...` and `go vet ./...` on a normal networked machine before publishing a release binary.

## ModLens references

Contract/reference used for this upgrade:

- <https://github.com/liustack/modlens/blob/main/skills/modlens/references/output-schema.md>
- <https://github.com/liustack/modlens/blob/main/README.zh-CN.md>
