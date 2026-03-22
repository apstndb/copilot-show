# Premium request billing model

`copilot-show` should treat `session.shutdown` as the authoritative source for closed-segment billing.

The durable conclusion from the local research corpus is:

- `session.shutdown.data.totalPremiumRequests` matches `sum(modelMetrics.*.requests.cost)`,
- `sum(modelMetrics.*.requests.count)` tracks assistant-message volume by model,
- the best local reconstruction of billing is **successive user-message windows with assistant output, weighted by the billed model multiplier**.

This is stronger than either of the simpler heuristics that came up during early exploration:

- raw `user.message` count is only a special case,
- raw `assistant.message` or `tool.execution_complete` counts are not billing metrics.

## Closed-segment rules

For closed segments, the practical accounting model is:

1. Split the event stream into segments bounded by `session.start` / `session.resume` and `session.shutdown`.
2. Within each closed segment, window the log by successive `user.message` rows.
3. Treat a window as a billable candidate only if it produces at least one `assistant.message` before the next `user.message` or `session.shutdown`.
4. Use `tool.execution_complete.data.model` when the window exposes a model directly.
5. If a window has assistant output but no model in local payloads, reconcile the residual assistant-message volume against shutdown `modelMetrics.*.requests.count`.
6. Sum the billed windows using the model multiplier for each assigned model.

This is the model that matched the durable local examples exactly.

## What `requests.count` and `requests.cost` mean

The research consistently separated two shutdown counters:

- `requests.cost` is the billed premium-request amount, including fractional multipliers.
- `requests.count` is closer to assistant-output volume grouped by model.

That split explains why a segment can show large raw request counts but relatively small billed premium usage.

It also explains why nested subagent work can appear in `requests.count` without adding premium cost.

In other words:

- `count` answers "how much assistant-output activity was attributed to this model in the closed segment?",
- `cost` answers "how much premium-request usage did shutdown bill to this model in the closed segment?"

Those values can diverge because discounted models, zero-output windows, and nested subagent work do not behave like a raw one-output-equals-one-bill counter.

## Zero-output and abort-only windows still matter

The durable billing model also needs one negative case: some `user.message` windows are structurally real, but reconcile as zero-cost.

The repeated local pattern was:

- a `user.message` starts a candidate window,
- no `assistant.message` appears before the next `user.message` or `session.shutdown`,
- the window may still contain `abort` rows or tool-only detours such as `tool.user_requested(local_shell)`.

For closed-segment reconciliation, those windows should usually be preserved as **zero-weight candidates**, not dropped as noise. They help explain why a segment can have more user prompts than billed premium requests while still matching shutdown totals exactly.

## Shutdown fields worth preserving in the repo

`session.shutdown` is valuable even when the immediate task is not premium-request accounting.

The durable fields worth keeping in mind are:

- `currentModel` for the segment's closing model state,
- `totalApiDurationMs` for model-work duration,
- `codeChanges.linesAdded`,
- `codeChanges.linesRemoved`,
- `codeChanges.filesModified`.

Those fields help with archaeology and later feature work because they survive even when:

- the Git worktree is noisy,
- the session is already closed,
- the local event stream is easier to inspect than the surrounding workspace history.

## Durable exact-fit examples

The precise session IDs are local scratch data, but the shape of the results is durable:

| Case | User windows | Reconstruction | Billed premium requests |
| --- | ---: | --- | ---: |
| Mixed model segment | 6 | `5 × GPT-5.4 + 1 × Claude Haiku 4.5` | `5.33` |
| Zero-output gap segment | 16 | `14 × GPT-5.4 + 2 zero-output windows` | `14.00` |
| Current-session closed segment | 44 | `44 × GPT-5.4` at the top level, while nested Claude Haiku 4.5 activity increases request counts without adding premium cost | `44.00` |

These examples are why `premium == user.message count` is too strong as a general statement.

It can be true in a segment where every top-level prompt is billable at `1.0×`, but it stops being true as soon as:

- a discounted model is used, or
- a zero-output / aborted window appears.

## What does not work as a primary billing join

The research ruled out several tempting shortcuts:

- `assistant.message` count overstates billing because one prompt can fan out into many assistant rows.
- `tool.execution_complete` count is an orchestration signal, not a billing signal.
- token volume does not explain premium totals in the inspected segments.
- `interactionId` is useful metadata, but not a safe standalone billing join key.
- `parentId` alone does not reconstruct user-to-assistant lineage reliably because many assistant-message parents are missing from the local JSONL.

## Implications for `copilot-show`

This supports the current `stats` strategy:

- if a session has `session.shutdown`, trust `totalPremiumRequests` and `modelMetrics.requests.{count,cost}`,
- if a session is still active and no shutdown exists yet, treat any estimate as heuristic.

The current fallback for active sessions is still intentionally crude.

If `copilot-show` grows a stronger live-session estimator later, it should be framed as **weighted billable user windows**, not raw tool completions.

## Reference query

Use `docs/research/weighted-user-trace.sql` when validating billing changes against local session logs with DuckDB.
