# copilot-show

`copilot-show` is a command-line tool to inspect GitHub Copilot information such as AI Credits usage, SDK quota signals, models, and tools.
It is built on top of [github.com/github/copilot-sdk/go](https://github.com/github/copilot-sdk).

## Documentation

- `docs/research/README.md`: curated research notes on experimental local AI-unit metrics, historical premium-request billing, session event-model caveats, and API pricing overrides.

## Installation

Install via `go install` (ensure your `GOPATH/bin` is in `PATH`):

```bash
go install github.com/apstndb/copilot-show@latest
```

Or using `mise`:

```bash
mise use -g go:github.com/apstndb/copilot-show@latest
```

### Usage

Run the tool with a subcommand to inspect specific resources.

Available subcommands:
- `quota`: Show the AI Credits allowance and other quota signals reported by the Copilot SDK. Use `usage` for detailed fractional consumption.
- `models`: Show models returned by the live Copilot SDK `ListModels` call, with runtime limits, reasoning support, policy state, and `$ I/O` token pricing.
- `model-docs`: Show docs-backed model metadata from `github/docs`, joined with the live CLI model list. Use `--all` for broader docs-backed fields, or `--visible-only` to hide docs rows your account cannot select right now.
- `tools`: Show available tools.
- `usage`: Show AI Credits billing usage from the GitHub API. Use `--with-pricing` to join SDK model token pricing, or `-f yaml` for billed amounts.
- `stats`: Show local usage statistics aggregated from session history, with optional API-equivalent cost estimates from token usage.
- `turns`: Show turn-by-turn usage statistics for a session.

### Common Options

Use `--format yaml` (or `-f yaml`) to get detailed raw data in YAML format.
Use `--table-mode ascii` or `--table-mode markdown` to change the table rendering style.
Use `--wrap-long-text` to show full description and summary columns with terminal-width wrapping instead of compact truncation.
Use `--discover` to enable automatic MCP and skill config discovery when commands create temporary sessions (for example `.mcp.json` and project skill directories).
Use `--show-hidden --help` to include hidden diagnostics commands and hidden flags in help output.

### Examples

### Quota Information

Shows the current AI Credits allowance and other quota signals reported by the Copilot SDK.
When the auth snapshot reports `token_based_billing: true`, or does not report the billing mode, the SDK still uses the raw key `premium_interactions`; `copilot-show` presents that value as AI Credits. An explicit request-based value keeps the Premium Interactions label.
Use `copilot-show usage` for detailed fractional consumption. Raw SDK field names remain available with `-f yaml`.

```bash
copilot-show quota
```

Example Output:
```text
--- Quota Information ---
┌─────────────┬──────────┬──────┬────────────┬─────────┐
│ Metric      │ Included │ Used │ Overage    │ Usage % │
├─────────────┼──────────┼──────┼────────────┼─────────┤
│ Chat        │        0 │    0 │ Disallowed │       - │
│ Completions │        0 │    0 │ Disallowed │       - │
│ AI Credits  │     1500 │    1 │ Disallowed │    0.1% │
└─────────────┴──────────┴──────┴────────────┴─────────┘
Last Updated: 2026-03-12T20:41:07+09:00

Notes:
- 'AI Credits' is sourced from the Copilot SDK's legacy-named `premium_interactions` snapshot.
- 'Used' is the SDK's whole-credit value; `copilot-show usage` shows detailed fractional consumption.
- Raw SDK field names remain available with `--format yaml`.
```

### List Models

Lists models returned by the local Copilot CLI server through the Copilot SDK `ListModels` API.
The table includes runtime details such as token limits, reasoning support, vision limits, policy `State`, and `$ I/O` per-million-token pricing from SDK billing metadata.
Use `-f yaml` for full SDK fields, including cache-read/write prices and docs catalog version metadata.

```bash
copilot-show models

# Try the latest github/docs tables for catalog version metadata
copilot-show models --latest
```

### Model Docs

Shows the docs-backed model matrix from `github/docs`, joined with the live CLI model list.
By default it shows docs-backed models that support Copilot CLI, including `Visible Now` and compact `Supported Plans`.
Use `--all` for the full docs catalog with summary counts, compact `Plans` and `Modes` columns, live `State`, and sorting that surfaces visible models first.
Retired-model history and `Live Models Without Docs Snapshot Match` appear after the main table.
`$ I/O` comes from live SDK billing metadata when a docs row matches a model in `ListModels`; otherwise it shows `-`.

```bash
copilot-show model-docs

# Try the latest github/docs tables first
copilot-show model-docs --latest

# Default CLI-focused table; hide docs rows not visible in ListModels
copilot-show model-docs --visible-only

# Include broader docs-backed metadata
copilot-show model-docs --all

# Full docs catalog plus live join details
copilot-show model-docs --all -f yaml
```

#### Finding models you cannot select right now

There is no separate Copilot SDK flag to list models outside your account entitlement.
`copilot-show` combines two sources instead:

| Goal | Command | What you get |
| --- | --- | --- |
| Everything your CLI server currently reports, including `policy.state` such as `disabled` | `copilot-show models` or `-f yaml` | Live SDK `ListModels` results only |
| Docs-backed models that do not support Copilot CLI | `copilot-show model-docs --all` | Docs rows with `Copilot CLI: No` |
| Docs-backed models your account does not currently see in `ListModels` | `copilot-show model-docs --all` | Docs rows with `Visible Now: No` |
| Runtime models missing from the docs snapshot | `copilot-show model-docs` | `Live Models Without Docs Snapshot Match` section |
| Models used in past local sessions but not visible now | `copilot-show stats` | Aggregated from `~/.copilot/session-state/*/events.jsonl` |

For the broadest static catalog, use `copilot-show model-docs --all -f yaml`.
Runtime billing and capability fields still come only from models returned by `ListModels`.

### List Tools

Lists built-in tools available to Copilot.

```bash
copilot-show tools
```

Use `--wrap-long-text` when you want the full tool description wrapped in the table instead of a shortened first line:

```bash
copilot-show tools --wrap-long-text
copilot-show skills --wrap-long-text
```

### Usage Report

Shows detailed AI Credits billing usage from the GitHub user API (requires `gh` CLI). This endpoint covers usage billed directly to the personal account; it excludes Copilot usage managed and billed through an organization or enterprise.
The default table focuses on **Used** credits grouped by Period and SKU with subtotals and included limits from quota snapshots when available.
Billed quantities and USD amounts remain available in YAML output (`-f yaml`).
Use `--with-pricing` to append a second table that joins each usage row with live Copilot SDK model token pricing (`$ I/O` per M tokens). Unmatched models show `-` in the pricing column.
With `--with-pricing -f yaml`, reports also include `withPricing`, `ioSummary`, and `tokenPricing` fields when a model match is found.
Supports relative date/month/year by specifying negative values (e.g., `-d -1` for yesterday).
Multiple periods can be shown with the `--last` flag.
The `Period` column can be sorted in ascending or descending order with the `--sort-order` flag (default is `desc`).

```bash
# Current month
copilot-show usage

# Join usage with live SDK model token pricing
copilot-show usage --with-pricing

# Full billing fields (including billed quantities and USD amounts)
copilot-show usage -f yaml

# Usage plus joined pricing in YAML
copilot-show usage --with-pricing -f yaml

# Yesterday's report
copilot-show usage -d -1

# Last 7 days daily report
copilot-show usage -d -1 --last 7

# Last 3 months monthly reports
copilot-show usage -m 0 --last 3
```

### Stats

Aggregates usage statistics from local session history (`~/.copilot/session-state/*/events.jsonl`).
Useful for understanding which models appear in local session events.
The default table reports model-attributed **Local AI Units** by normalizing the SDK's experimental `session.shutdown.data.modelMetrics.*.totalNanoAiu` values (`10^9` nano-AIU = one displayed local unit). The SDK describes these values as session-wide accumulated metering, so `stats` uses the latest shutdown snapshot in each selected session instead of summing successive resume/shutdown snapshots. The current-month filter selects sessions by shutdown time, so a session-wide value can include activity from before that month. A `-` means that the selected closed sessions predate or omit this field; an explicit zero remains `0`. The top-level `totalNanoAiu` total and model-attributed aggregate are both available in YAML, and the table warns if they differ.

Local AI Units are not GitHub billing AI Credits and are not billing authority. Use `copilot-show usage` for AI Credits billed directly to the personal account; organization- or enterprise-managed Copilot usage is outside that user endpoint.
The hidden `--ui-version old` compatibility view retains the historical premium-request columns for older logs and research.
Use `--api-costs` to estimate equivalent API cost from shutdown token usage. When shutdown metrics contain `cacheReadTokens`, the estimate treats them as a subset of `inputTokens`, prices only the uncached remainder at the full input-token rate, and prices cached reads separately when the selected model has a verified cached-input price. The default table lays out token kinds, token counts, `USD/Mtok` rates, and cost subtotals as aligned multiline cells within each model row, and its `Input Tokens` row shows the billed uncached remainder rather than the raw total input. If future shutdown logs add extra token-usage keys such as `cacheOutputTokens`, the new table will add an extra row for that model automatically; those rows remain unpriced until explicit API-pricing support is added.
Models without matching API pricing still remain in the output; their API-cost cells stay empty and the printed total becomes a lower bound.
Model availability is still plan-dependent, so local shutdown metrics can contain model IDs that are not currently visible in `copilot-show models`.
Use `--api-pricing <file>` to overlay those built-in public prices with local YAML values. This is intended as a safe starting point for local effective-price overrides, without baking private contract terms into the tool.
Omitted fields inherit the built-in catalog. If you add a model that is not in the built-in catalog, define at least `inputUsdPerMToken` and `outputUsdPerMToken`.
Use `--api-pricing-template` to dump a commented starter file with every built-in model and known value.

```bash
copilot-show stats [-a]

# Compare local AI Units with API-equivalent token cost
copilot-show stats --api-costs

# Write a commented starter file you can edit locally
copilot-show stats --api-pricing-template > ~/.copilot/api-pricing.yaml

# Apply local API price overrides (implies --api-costs)
copilot-show stats --api-pricing ~/.copilot/api-pricing.yaml
```

Example override file:

```yaml
models:
  gpt-5.4:
    inputUsdPerMToken: 1.50
    outputUsdPerMToken: 12.00
  claude-opus-4.6:
    inputUsdPerMToken: 4.00
    outputUsdPerMToken: 20.00
```

### Turns

Displays turn-by-turn usage statistics for a specific session.
The command reconstructs assistant turn windows from local event history and shows:

- chronological `Turn #`
- `Segment` numbers that increment on `session.start` / `session.resume`
- the raw Copilot `Turn ID`, which can repeat within one session

```bash
copilot-show turns [sessionID]
```

### YAML Output

All commands support `-f yaml` flag to output detailed data in YAML format.

```bash
copilot-show quota -f yaml
```

## Hidden Subcommands

The following commands are hidden by default but can be executed by specifying their names:

- `agents`: List available Copilot agents
- `skills`: List available skills (name, source, enabled, path, description)
- `extensions`: List available extensions (id, status, source, pid)
- `plugins`: List installed plugins (name, marketplace, version)
- `mcp`: List configured MCP servers (name, status, source)
- `mcp-config`: List user-configured MCP servers from Copilot settings
- `discover`: Unified discovery snapshot from server-scoped SDK APIs (MCP, skills, agents, instructions, and canonical discovery paths)
- `account`: Show account authentication and registered users
- `session-metadata [sessionID]`: Show session metadata snapshot (model, mode, workspace)
- `session-usage [sessionID]`: Show live per-session usage metrics, including experimental Local AI Units normalized from `TotalNanoAiu` (`TotalNanoAiu` / 10⁹)
- `session-auth [sessionID]`: Show per-session authentication status
- `current-model`: Show the currently selected model ID
- `current-agent`: Show the currently selected agent
- `mode`: Show the current agent mode
- `plan`: Read the current plan file
- `workspace`: List files in the workspace
- `read-file <path>`: Read a specific file from the workspace
- `ping`: Check connection to the server
- `status`: Show CLI version and authentication status
- `sessions`: List all sessions with PID information
- `history [sessionID]`: Show conversation history (from local event logs). The default `--view raw` keeps the event-by-event timeline in fixed `Time / Delta / Event` columns. `--view spans` pairs `tool.execution_start` and `tool.execution_complete` by `toolCallId`, and `--group-by turn` folds `Assistant Turn Start/End` into synthetic turn headers. In this grouped mode, `User` and `Turn` are shown at the same structural level under each interaction, while `Assistant` and `Tool` rows remain nested under the turn. The table also switches to `Time / Span / Structure / Detail` so the hierarchy stays visually aligned.
- `graph [sessionID]`: Show a graph-oriented summary of local event logs, including `parentId` gaps, direct `interactionId` hubs, and nested `parentToolCallId` groupings.
- `resume-branches [sessionID]`: Trace inferred work branches that start from `session.resume`
- `validate-events [sessionID]`: Validate local session events against copilot-sdk generated types

## History Event Reference

`history` reads local `~/.copilot/session-state/<session>/events.jsonl` rows and projects them in three ways:

- `--view raw`: event-by-event timeline in `Time / Delta / Event`
- `--view spans`: tool start/end pairs collapsed into `Time / Span / Event`
- `--view spans --group-by turn`: turn-aware layout in `Time / Span / Structure / Detail`

The following event types currently have explicit formatting:

| Event type | `--view raw` label | `--view spans` label | `--view spans --group-by turn` | Detail source / notes |
| --- | --- | --- | --- | --- |
| `session.start` | `Session Start` | `Session Start` | `Session Start` | Shows `CWD: ...` when `data.context.cwd` is present. |
| `session.resume` | `Session Resume` | `Session Resume` | `Session Resume` | No extra detail. |
| `session.context_changed` | `Context Changed` | `Context Changed` | `Context Changed` | Summarizes repository and branch when present, with extra lines for `cwd`, `gitRoot`, `headCommit`, and `baseCommit`. |
| `session.compaction_start` | `Compaction Start` | `Compaction Start` | `Compaction Start` | Marks the beginning of a session compaction pass. |
| `session.compaction_complete` | `Compaction Complete` | `Compaction Complete` | `Compaction Complete` | Shows checkpoint number and success status, with extra lines for token counts, checkpoint path, request ID, and a truncated summary snippet. |
| `session.mode_changed` | `Mode Changed` | `Mode Changed` | `Mode Changed` | Shows `previousMode -> newMode`. |
| `session.model_change` | `Model Changed` | `Model Changed` | `Model Changed` | Shows `previousModel -> newModel` when both are present, plus reasoning effort as an extra line. |
| `session.info` | `Session Info` | `Session Info` | `Session Info` | Shows `infoType: message` when both are present. |
| `session.workspace_file_changed` | `Workspace File Changed` | `Workspace File Changed` | `Workspace File Changed` | Shows the workspace file operation and path. |
| `user.message` | `User` | `User` | `User` | Shows the user message text. In grouped mode, `User` is a peer of `Turn` under the current interaction. |
| `assistant.turn_start` | `Assistant Turn Start` | `Assistant Turn Start` | Replaced by synthetic `Turn` row | Reconstructed as `Turn #`, `Segment`, raw `Turn ID`, and resolved interaction shorthand. |
| `assistant.message` | `Assistant` | `Assistant` | `Assistant` | Shows the assistant message text. In grouped mode it is nested under the current `Turn`. |
| `tool.user_requested` | `Tool Requested` | `Tool Requested` | `Tool Requested` | Shows the requested tool and a compact argument summary before execution starts. |
| `tool.execution_start` | `Tool Start` | Folded into synthetic `Tool` span | Folded into synthetic `Tool` span | Raw mode shows `toolName` or `<unknown>`. In spans modes it is paired by `toolCallId`. |
| `tool.execution_complete` | `Tool End` | Folded into synthetic `Tool` span | Folded into synthetic `Tool` span | Raw mode shows tool name, optional model, and `Success: ...`. In spans modes it completes the paired tool row. |
| `assistant.turn_end` | `Assistant Turn End` | `Assistant Turn End` | Suppressed; duration moves to synthetic `Turn` row | Raw/spans show reconstructed turn number and duration. |
| `session.shutdown` | `Session Shutdown` | `Session Shutdown` | `Session Shutdown` | Shows historical premium-request and per-model request/cost/token details. Use `stats` for experimental Local AI Units derived from `totalNanoAiu`. |
| `skill.invoked` | `Skill Invoked` | `Skill Invoked` | `Skill Invoked` | Uses `data.name`, falling back to `data.skillName`. |
| `subagent.started` | `Subagent Started` | `Subagent Started` | `Subagent Started` | Uses `agentDisplayName`, falling back to `agentName`. |
| `subagent.completed` | `Subagent Completed` | `Subagent Completed` | `Subagent Completed` | Uses `agentDisplayName`, falling back to `agentName`. |
| `system.notification` | `System Notification` | `System Notification` | `System Notification` | Uses `data.kind.type`, then `data.type`, then `notification`. |
| `session.plan_changed` | `Plan Changed` | `Plan Changed` | `Plan Changed` | No extra detail. |
| `abort` | `Abort` | `Abort` | `Abort` | No extra detail. |
| anything else | `Event` | `Event` | `Event` | Fallback path: shows the raw event type and appends `(id)` when available. |

Some rows in the history output are synthetic and do not come directly from a single `type` value:

| Synthetic row | Where it appears | Meaning |
| --- | --- | --- |
| `Interaction` | `--view raw`, `--view spans`, grouped spans | Inserted when the resolved `interactionId` changes. This is a renderer aid, not a stored event row. |
| `Tool` | `--view spans`, grouped spans | Built by pairing `tool.execution_start` and `tool.execution_complete` on `toolCallId`. The detail includes duration and completion state such as complete, open, or end-without-start. |
| `Turn` | `--view spans --group-by turn` | Built from reconstructed `assistant.turn_start` / `assistant.turn_end` windows. It replaces the raw turn boundary rows in grouped mode. |

## License

MIT License
