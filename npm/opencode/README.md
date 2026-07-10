# @witness-ai/opencode

OpenCode plugin for witness. It reconciles OpenCode's local session database on startup and when a session goes idle, and starts witness distillation only through the CLI's laptop-friendly auto-start gate.

## Install

Add the npm plugin to your OpenCode config. OpenCode installs the package automatically with Bun on startup, and the plugin auto-registers `mcp.witness` if you have not already defined one:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "plugin": ["@witness-ai/opencode"]
}
```

Optional: install it globally if you also want a `witness` CLI on your shell `PATH`:

```sh
npm install -g @witness-ai/opencode
```

Optional: run the CLI ad hoc without a global install:

```sh
npm exec --yes --package=@witness-ai/opencode -- witness doctor
```

The package includes the `witness` CLI, a bundled platform binary, and prompts. The npm tarball is about 96MB, and the first model download is about 470MB into the witness data directory (`assets/e5-small`). The OpenCode plugin starts that model download only while OpenCode is running; if OpenCode stops before it finishes, the plugin stops its downloader and retries later with bounded backoff. The plugin uses the bundled binary first, then falls back to `WITNESS_BIN` or `witness` from `PATH`.

By default the model is downloaded from Hugging Face. A custom `WITNESS_MODEL_BASE_URL` must serve the same paths (`onnx/model.onnx` and `tokenizer.json`) and also set `WITNESS_MODEL_SHA256` plus `WITNESS_TOKENIZER_SHA256`.

Automatic distillation is batched by default: sessions are reconciled on startup and idle, while model work starts at most once every 10 minutes, drains the current queue, then exits so the embed model is not resident. Edit witness `config.toml` if you want manual-only behavior:

```toml
auto_distill = false
```

If you want to force a different binary, set `WITNESS_BIN` before starting OpenCode:

```sh
export WITNESS_BIN=/absolute/path/to/witness
```

If you already have your own `mcp.witness` entry, the plugin leaves it alone. The npm CLI deliberately does not support `witness install` / `witness uninstall`; those are source-checkout workflows, not the npm one.
