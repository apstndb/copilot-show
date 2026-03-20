# copilot-show

`copilot-show` is a command-line tool to inspect various information from the GitHub Copilot CLI, such as quotas, models, and tools.
It is built on top of [github.com/github/copilot-sdk/go](https://github.com/github/copilot-sdk).

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
- `quota`: Show Copilot Premium Interactions usage and entitlement.
- `models`: Show available models and their multipliers.
- `tools`: Show available tools.
- `usage`: Show billing usage report from GitHub API, with model multipliers and entitlement joined from the models list and quota snapshots.
- `stats`: Show local usage statistics aggregated from session history, with optional API-equivalent cost estimates from token usage.
- `turns`: Show turn-by-turn usage statistics for a session.
- `sessions`: List recent Copilot CLI sessions.
- `history`: Show event history for a session.

### Common Options

Use `--format yaml` (or `-f yaml`) to get detailed raw data in YAML format.
Use `--table-mode ascii` or `--table-mode markdown` to change the table rendering style.

### Examples

### Quota Information

Shows the current usage and entitlement of Copilot Premium Interactions.

```bash
copilot-show quota
```

Example Output:
```text
--- Quota Information ---
┌──────────────────────┬─────────────┬──────┬────────────┬─────────┐
│ METRIC               │ ENTITLEMENT │ USED │ OVERAGE    │ USAGE % │
├──────────────────────┼─────────────┼──────┼────────────┼─────────┤
│ chat                 │           0 │    0 │ Disallowed │ -       │
│ completions          │           0 │    0 │ Disallowed │ -       │
│ premium_interactions │         300 │   65 │ Disallowed │ 21.7%   │
└──────────────────────┴─────────────┴──────┴────────────┴─────────┘
Last Updated: 2026-03-12T20:41:07+09:00

Plan Reference (Approximate Monthly Entitlement):
- Copilot Free: 50
- Copilot Pro / Business: 300
- Copilot Enterprise: 1,000
- Copilot Pro+: 1,500

Month Progress (UTC): 38.8%
...
```

### List Models

Lists available AI models with details like context size and billing multipliers.

```bash
copilot-show models
```

### List Tools

Lists built-in tools available to Copilot.

```bash
copilot-show tools
```

### Usage Report

Shows detailed billing usage for premium requests from the GitHub API (requires `gh` CLI).
Displays grouped results by Period and SKU with subtotals, multipliers, and entitlement information.
Supports relative date/month/year by specifying negative values (e.g., `-d -1` for yesterday).
Multiple periods can be shown with the `--last` flag.
The `Period` column can be sorted in ascending or descending order with the `--sort-order` flag (default is `desc`).

```bash
# Current month (default)
copilot-show usage

# Yesterday's report
copilot-show usage -d -1

# Last 7 days daily report
copilot-show usage -d -1 --last 7

# Last 3 months monthly reports
copilot-show usage -m 0 --last 3
```

### Stats

Aggregates usage statistics from local session history (`~/.copilot/session-state/*/events.jsonl`).
Useful for understanding which models are consuming your quota.
The `Premium Requests (Cost)` column preserves fractional multipliers such as `0.33`.
Use `--api-costs` to estimate equivalent API cost from shutdown token usage, including cached token reads when the selected model has a verified cached-input price.
Model availability is still plan-dependent, so local shutdown metrics can contain model IDs that are not currently visible in `copilot-show models`.

```bash
copilot-show stats [-a]

# Compare premium-request overage vs. API-equivalent token cost
copilot-show stats --api-costs
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
| `session.shutdown` | `Session Shutdown` | `Session Shutdown` | `Session Shutdown` | Shows total premium requests, plus per-model request/cost/token lines when `modelMetrics` exists. |
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
