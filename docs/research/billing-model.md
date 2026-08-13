# Local AI-unit and historical premium-request metrics

## Nano-AIU in shutdown metrics

Newer SDK session logs can record experimental metering as nano-AI units in both `session.shutdown.data.totalNanoAiu` and `session.shutdown.data.modelMetrics.*.totalNanoAiu`. For readable local output, `copilot-show` normalizes 1,000,000,000 nano-AIU to one **Local AI Unit**. Neither the SDK contract nor GitHub billing documentation establishes that this local unit equals one billed AI Credit.

These fields can be absent from older sessions. Absence must remain distinct from an explicit zero. The SDK describes shutdown `totalNanoAiu` as a session-wide accumulated value, so a resumed session can emit multiple snapshots of the same cumulative meter. `stats` therefore keeps the latest selected shutdown snapshot for each session and then sums those per-session values. Its current-month filter selects that snapshot by shutdown time; a session-wide value can include activity from before the selected month. Historical premium-request fields are handled separately because inspected local logs show that those values can reset at resume boundaries.

In the inspected local corpus, every shutdown row that had a top-level nano-AIU value also had model values whose sum matched it exactly. The default `stats` table deliberately uses the model sum so its headline matches the displayed rows, retains both aggregates in YAML, and warns if they differ.

Local shutdown metrics are useful for attributing completed-session activity to models. They are not the billing source of truth: `copilot-show usage` reads the GitHub user billing endpoint for usage billed directly to the personal account. That endpoint excludes Copilot usage managed and billed through an organization or enterprise. Current-session tails also do not contribute `totalNanoAiu` until a shutdown metric is written.

Use `.github/skills/duckdb-jsonl-inspection/queries/26-shutdown-local-ai-unit-summary.sql` to compare top-level and per-model values without reading message content.

## Historical premium-request model

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

This supports the historical compatibility view in `stats`:

- if a session has `session.shutdown`, trust `totalPremiumRequests` and `modelMetrics.requests.{count,cost}` for legacy premium-request analysis,
- if a session is still active and no shutdown exists yet, treat any estimate as heuristic.

The current fallback for active sessions is still intentionally crude.

If `copilot-show` grows a stronger live-session estimator later, it should be framed as **weighted billable user windows**, not raw tool completions.

## Reference query

Use `docs/research/weighted-user-trace.sql` when validating billing changes against local session logs with DuckDB.
