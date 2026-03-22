# Research notes

These docs distill local investigation into durable repository notes for `copilot-show`.

They keep the conclusions, caveats, and reusable query patterns that matter for the tool, while dropping per-session UUIDs, line-number archaeology, and other scratch material that was only useful during investigation.

## Curated docs

| Repo doc | Purpose |
| --- | --- |
| `docs/research/billing-model.md` | Explain how premium requests reconcile against session shutdown metrics and weighted user-message windows. |
| `docs/research/event-types-reference.md` | Catalog the local `events.jsonl` event types that `copilot-show` currently recognizes and the fields that matter for billing, turn windows, and nested tool lineage. |
| `docs/research/session-event-model.md` | Capture event-model caveats for resume boundaries, turn IDs, interaction IDs, parent links, and nested subagent spans. |
| `docs/research/api-pricing-overrides.md` | Explain why `copilot-show` keeps public list prices in-repo and uses local YAML overrides for account-specific effective prices. |
| `docs/research/weighted-user-trace.sql` | Provide a reusable DuckDB query template for reconstructing weighted billing windows from local session logs. |

## What changed in the repo

The pricing research now directly informs `stats --api-costs`:

- built-in public token prices stay in the repository,
- local effective prices can override them with `--api-pricing`,
- a commented starter file is available via `--api-pricing-template`.

That design keeps contract-specific terms out of the codebase while still making serious local cost comparison possible.
