# Session event types reference

This doc curates the local `events.jsonl` event types that `copilot-show` currently recognizes in `history`, `turns`, and `graph`.

It is not a guarantee that every CLI build emits only these rows. It is the durable subset that already matters in this repository.

For per-event field semantics, the most precise upstream references are currently:

- `github/copilot-sdk/docs/features/streaming-events.md` for the human-readable event reference,
- `github/copilot-sdk/nodejs/src/generated/session-events.ts` for the closest checked-in schema shape,
- `github/copilot-sdk/go/generated_session_events.go` for Go field-name parity, but not as a proof that every optional `Data` field applies to every event type.

## Boundary and billing events

| Event types | What they mark | Fields worth keeping | Practical notes |
| --- | --- | --- | --- |
| `session.start`, `session.resume` | Start a new local segment | `id`, `parentId`, `data.eventCount` on resume | `session.resume` is a continuity checkpoint. Graceful resumes usually point `parentId` at the previous `session.shutdown`. When `data.alreadyInUse == true`, an intentional concurrent resume can instead point `parentId` at the last persisted event while `data.eventCount` still matches the prior row count; some non-graceful recoveries can use the same parent shape without the in-use signal. Segment-local turn tracking resets here either way. |
| `session.shutdown` | Close a segment and summarize it | `data.totalNanoAiu`, `data.totalPremiumRequests`, `data.modelMetrics`, `data.currentModel`, `data.totalApiDurationMs`, `data.codeChanges.*` | `totalNanoAiu` is an optional experimental session-wide local metering snapshot and is not asserted to equal billed AI Credits; `totalPremiumRequests` is the authoritative historical premium-request value for the closed segment. The GitHub user billing endpoint remains authoritative for usage billed directly to the personal account. |
| `abort` | Explicitly interrupted work in the active segment | `data.turnId`, `data.interactionId` when present | Useful for turn state and archaeology. A user window that aborts before any `assistant.message` can still be a real zero-weight billing candidate. |

## User and assistant events

| Event types | What they mark | Fields worth keeping | Practical notes |
| --- | --- | --- | --- |
| `user.message` | Start of a new top-level prompt window | message text, `interactionId` when present | Use successive `user.message` windows when reconstructing billable candidates locally. Some windows later reconcile as zero-weight if no assistant output appears before the next boundary. |
| `assistant.turn_start`, `assistant.turn_end` | Turn scaffold used by `turns` / `history` | `turnId`, `interactionId`, `parentId` | `turnId` resets per segment, so it is not a session-global counter. |
| `assistant.message` | Actual assistant output row | message text, `interactionId` when present | This is evidence of output, but `parentId` is often missing or dangling, so it is not a safe standalone lineage join. |

## Tool, skill, and nested-subagent events

| Event types | What they mark | Fields worth keeping | Practical notes |
| --- | --- | --- | --- |
| `tool.execution_start`, `tool.execution_complete` | Tool span boundaries | `toolCallId`, `parentToolCallId`, `toolName`, `model` when present | `tool.execution_complete` often provides the best local model tag for a billed window. `parentToolCallId` is the strongest join for nested tool/subagent lineage. |
| `tool.user_requested` | User-approved or user-triggered tool action | `toolCallId`, `toolName` | Control-plane signal, not a billing signal. |
| `skill.invoked` | Skill activation | `name` or `skillName`, `toolCallId` | Useful for understanding orchestration, especially around nested work. |
| `subagent.started`, `subagent.completed` | Nested agent span boundaries | `toolCallId`, `parentToolCallId`, `agentDisplayName`, `agentType` | These rows are important for control-plane archaeology. Nested work can raise shutdown `requests.count` without increasing `requests.cost`. |

## Capability and remote-control events

| Event types | What they mark | Fields worth keeping | Practical notes |
| --- | --- | --- | --- |
| `capabilities.changed` | SDK/CLI capability surface changed mid-session | `data.ui.elicitation` | In `copilot-sdk/go v0.2.1`, this is the new event family that reflects UI capability changes such as elicitation support. It is control-plane state, not a billing unit. |
| `sampling.requested`, `sampling.completed` | MCP sampling request lifecycle | `serverName`, `mcpRequestId`, `model`, `initiator` | These rows matter when an MCP server asks Copilot CLI to perform model sampling on its behalf. They should be kept separate from top-level user billing rows. |
| `session.custom_agents_updated` | Custom-agent catalog refresh | `data.agents`, `data.warnings`, `data.errors` | Useful when agent availability changes during a long-running session or after extension reloads. |
| `session.remote_steerable_changed` | Mission Control remote-steering support changed | `data.remoteSteerable` | Capability flag rather than a user action. |

## Context and housekeeping events

| Event types | What they mark | Fields worth keeping | Practical notes |
| --- | --- | --- | --- |
| `session.context_changed` | Session context mutation | context-specific payload in `data` | Useful when explaining why later prompts see different context. |
| `session.model_change` | Model switch | current / next model in `data` | Contextual metadata rather than a billing unit. |
| `session.mode_changed` | Mode switch | mode fields in `data` | Explains later tool and prompt behavior. |
| `session.plan_changed` | Plan switch | plan fields in `data` | Important when availability or billing expectations change during a session. |
| `session.workspace_file_changed` | Workspace mutation | file path fields in `data` | Useful archaeology for long sessions. |
| `session.info` | Informational session metadata | arbitrary info in `data` | Context row rather than an action row. |
| `session.compaction_start`, `session.compaction_complete` | Transcript compaction boundaries | compaction metadata in `data` | Useful when explaining history rewrites or context resets. |
| `system.notification` | Runtime or UI notice | notification text in `data` | Operator-facing metadata, not billing. |

## Relationship fields to keep separate

Several IDs appear together in the same stream, but they should not be treated as interchangeable:

- `id` / `parentId` describe the event graph,
- `toolCallId` / `parentToolCallId` describe tool-span lineage,
- `turnId` is segment-local turn scaffolding,
- `interactionId` is useful grouping metadata but not a safe standalone billing join.

That distinction is the reason `copilot-show graph` tracks both event-parent edges and tool-parent edges.

## Unknown and future events

`copilot-show` should not assume that the current explicit switch statements are a complete universe of possible rows.

The current strategy is intentionally layered:

- keep local JSONL parsing schema-light instead of hard-binding to the current SDK enum set,
- note that `copilot-sdk/go v0.2.2` now preserves unknown event payloads as `RawSessionEventData`, so "unknown to the current enum set" and "rejected by the SDK parser" are no longer the same question,
- treat the raw `type` string as authoritative even when the repository does not have a dedicated renderer for it yet,
- use best-effort history fallbacks that surface common fields like `content`, `message`, `toolName`, `path`, `requestId`, `toolCallId`, and `interactionId`,
- keep structural joins conservative: `graph` and turn reconstruction still rely only on top-level `interactionId`, `toolCallId`, and `parentToolCallId` fields that are already established locally.

That means a newer Copilot CLI build can show new rows in `history` before `copilot-show` learns first-class labels for them, without silently dropping the events or inventing speculative lineage from arbitrary nested payloads.

## Cross-references

- Use `docs/research/session-event-model.md` for resume boundaries, dangling parents, and nested-span caveats.
- Use `docs/research/billing-model.md` for shutdown accounting and the meaning of `requests.count` versus `requests.cost`.
