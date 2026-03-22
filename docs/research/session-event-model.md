# Session event model notes

The billing research also uncovered several durable facts about the local session event model.

These are useful when working on `history`, `turns`, `graph`, or any future session-analysis features.

For the current curated list of event families that `copilot-show` explicitly recognizes, see `docs/research/event-types-reference.md`.

## Segment boundaries and resume integrity

The inspected logs support treating session boundaries as structured segments:

- each segment starts at `session.start` or `session.resume`,
- each closed segment ends at `session.shutdown`,
- `session.resume.parentId` matched the immediately preceding `session.shutdown.id` in the inspected restart,
- `session.resume.data.eventCount` matched the exact number of rows that existed before the resume boundary.

That makes `session.resume` a strong continuity checkpoint rather than a loose informational marker.

It also explains why `buildTurnWindows` resets segment-local turn tracking on `session.start` / `session.resume`: the local stream treats those rows as hard boundaries, not soft hints.

For future validation work, the useful rule is: if a resumed segment does **not** satisfy `resume.parentId == previous shutdown.id` or `resume.data.eventCount == rows_before_resume`, treat that session as suspicious and surface a warning instead of silently trusting the continuation.

## `turnId` and `interactionId` caveats

Two important identifiers are less stable than they first appear:

- raw `turnId` resets per segment, so it is not a session-global counter,
- `interactionId` can drift across a user window, so a billed user prompt does not always receive assistant activity under the same interaction ID.

Because of that, `interactionId` is useful metadata for grouping and display, but not a safe primary join key for billing reconstruction.

One durable pattern is steering during an already-open assistant turn: a new `user.message` can carry a fresh `interactionId`, while the downstream assistant rows continue under the earlier still-open interaction. That should be treated as a real control-flow pattern, not immediate evidence of corrupted local logs.

## `parentId` is incomplete for local lineage

The local JSONL files often contain dangling `parentId` links for `assistant.message` rows.

That means pure parent-lineage reconstruction is incomplete for:

- user-to-assistant joins,
- assistant-message ancestry,
- some tool-to-message relationships.

When the goal is billing or durable attribution, the safer local method is still successive user-message windows plus shutdown reconciliation.

## `parentId` and `parentToolCallId` are different graphs

The local event model carries two different lineage systems:

- `parentId` links one event row to another event row by `id`,
- `toolCallId` groups tool-span events that belong to the same tool call,
- `parentToolCallId` links a child tool span to its parent tool span.

Those relationships are orthogonal rather than interchangeable.

In practice:

- `parentId` is best for reconstructing the event DAG when the rows are present,
- `parentToolCallId` is best for reconstructing nested `task` / subagent control flow,
- an event can have a useful `parentToolCallId` even when `parentId` is missing or unhelpful.

That is why the graph code keeps both event-parent edges and tool-parent edges instead of trying to collapse them into one structure.

## `parentToolCallId` is valuable for nested work

One inspected segment showed a useful pattern: Claude Haiku 4.5 activity appeared only inside a nested subagent span, and shutdown later reported non-zero `requests.count` but zero `requests.cost` for that model. In that situation, `parentToolCallId` was the most useful local join for tracing the nested work.

In the inspected example:

- Claude Haiku 4.5 activity appeared only inside one nested `task` / `explore` subagent span,
- the nested span produced assistant messages and model-tagged tool completions,
- shutdown recorded non-zero `requests.count` for Haiku but zero `requests.cost`.

The strongest explanation is that nested subagent work was counted as assistant-output volume but did not cross the billing boundary for a separate top-level prompt.

So `parentToolCallId` is important for control-plane lineage even when it does not define billing directly.

In practical terms, nested model activity with `requests.count > 0` and `requests.cost == 0` should be read as "visible local work that still fit inside an already-billed top-level prompt," not as proof of extra hidden billable prompts.

## `session.shutdown` carries more than billing

`session.shutdown` is also a useful archaeological summary of the segment.

In addition to billing fields, it can carry:

- `currentModel`,
- `totalApiDurationMs`,
- `codeChanges.linesAdded`,
- `codeChanges.linesRemoved`,
- `codeChanges.filesModified`.

That `codeChanges` block is worth remembering because it can summarize a closed work segment even if Git history is noisy or incomplete.

`totalApiDurationMs` is also useful when a later command needs elapsed-model-work context but only has the local event log available.

## Upstream references and forward-compatible parsing

For newly observed rows, the most useful upstream references are currently the Copilot SDK's `docs/features/streaming-events.md` guide and the checked-in TypeScript discriminated union in `nodejs/src/generated/session-events.ts`.

The generated Go file is still useful, but it intentionally flattens many event payloads into one large optional-field `Data` struct. That makes it good for field-name parity and parser glue, but weaker as a per-event semantics reference.

That distinction matters when `copilot-cli` starts emitting new event types before `copilot-sdk` or this repository grows explicit support for them. The safer local strategy is:

- parse rows as generic JSON objects instead of hard-failing on unfamiliar event types,
- preserve the raw `type` string and common control IDs,
- render unknown rows with best-effort summaries first,
- add first-class labels only after local evidence or upstream docs justify a narrower interpretation.

In other words, "show the row conservatively" is better than either dropping it or overfitting lineage to a guessed schema.

## Practical guidance for `copilot-show`

When adding or changing session-analysis commands:

- prefer shutdown metrics over inferred counts whenever a closed segment is available,
- treat `interactionId` as display metadata, not authoritative lineage,
- expect missing `parentId` joins for assistant messages,
- use `parentToolCallId` when tracing nested `task` / subagent activity,
- keep "request count" and "premium cost" conceptually separate.

Those distinctions made the later `history`, `turns`, `graph`, and `stats` work more reliable.
