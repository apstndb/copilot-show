# Agent Context: copilot-show

This project, `copilot-show`, is a CLI tool designed to explore and expose internal information from the GitHub Copilot CLI via its Go SDK.

## Project Goal
Provide a transparent view into Copilot's runtime state, including:
- Quota usage (Premium Interactions).
- Available AI models and their specific capabilities (context window, SDK `$ I/O` token pricing; `billingMultiplier` remains YAML-only when present).
- Built-in tools and their configurations.
- Session-specific data like current agents, modes, and workspace files.

## Tech Stack
- **Language**: Go
- **Core Library**: [github.com/github/copilot-sdk/go](https://github.com/github/copilot-sdk)
- **CLI Framework**: [github.com/spf13/cobra](https://github.com/spf13/cobra)
- **Table Rendering**: [github.com/olekukonko/tablewriter](https://github.com/olekukonko/tablewriter)
- **YAML Processing**: [github.com/goccy/go-yaml](https://github.com/goccy/go-yaml)

## Implementation Details
- The tool interacts with the local Copilot CLI server (started/managed by the SDK) for online commands; session-log commands (`stats`, `turns`, `history`, `graph`, `validate-events`, `resume-branches`) read `~/.copilot/session-state` offline.
- `usage` shells out to `gh api` and requires GitHub CLI authentication; most other subcommands use the user's existing Copilot login session.
- Most online subcommands utilize a temporary session created via `client.CreateSession`.
- Session event semantics, billing caveats, and research notes live in [docs/research/](docs/research/README.md) — read before changing `history`, `turns`, `stats`, or graph logic.
- DuckDB SQL workflows for inspecting local session JSONL live in [.github/skills/duckdb-jsonl-inspection/](.github/skills/duckdb-jsonl-inspection/SKILL.md).

## Local verification
- Run `go generate ./...` and verify that it leaves generated files unchanged.
- Run `go test ./...`, `go vet ./...`, and `go build ./...` before submitting changes.
- Install via `go install github.com/apstndb/copilot-show@latest` or `mise use -g go:github.com/apstndb/copilot-show@latest` (see README).

## Release publication
- Pushing a `v*` tag runs the release workflow, which verifies the source, builds assets, and creates a draft GitHub release.
- Replace the draft body with reviewed per-version notes using `gh release edit <tag> --notes-file <file>`, then publish it with `gh release edit <tag> --draft=false`.
- Verify the published release with `gh release view <tag>` and `GOPROXY=direct go list -m github.com/apstndb/copilot-show@<tag>`.

## Instructions for Agents
- When modifying this project, maintain consistent table layouts using `render.CreateTable` (pkg/render).
- Ensure all output-related subcommands support both `table` and `yaml` formats via the `--format` flag.
- Keep "useful but cluttered" or internal-only information under `Hidden: true` subcommands.
- Follow Go best practices and ensure `go mod tidy` is run after adding dependencies.
- Never introduce new abbreviations in the code or UI without explicit user permission. (Exception: 'requests' may be abbreviated as 'req.' in table headers).
- Use user-facing terminology in table headers (e.g., 'Used', 'Billed' instead of 'Gross', 'Net' in usage reports). Use 'Included' instead of 'Entitlement' for premium request limits.
- All significant UI modifications (changes to table layouts, sorting, or new display formats) should support temporary A/B testing while they are being validated.
- Use a reversible mechanism during that validation period so the old and new implementations can be compared side by side.
- Once the new implementation is verified, keep a single default user-facing path.
- If the A/B mechanism will be reused, keep the toggle hidden rather than removing and re-adding it.
- Document the changes and the verification results when retiring or hiding the temporary A/B implementation.
- `--ui-version` (hidden) still toggles stats table layout and table fold behavior; history A/B was retired 2026-07-05 (new layout only).
