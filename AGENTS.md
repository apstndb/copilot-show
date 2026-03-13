# Agent Context: copilot-show

This project, `copilot-show`, is a CLI tool designed to explore and expose internal information from the GitHub Copilot CLI via its Go SDK.

## Project Goal
Provide a transparent view into Copilot's runtime state, including:
- Quota usage (Premium Interactions).
- Available AI models and their specific capabilities (context window, billing multipliers).
- Built-in tools and their configurations.
- Session-specific data like current agents, modes, and workspace files.

## Tech Stack
- **Language**: Go
- **Core Library**: [github.com/github/copilot-sdk/go](https://github.com/github/copilot-sdk)
- **CLI Framework**: [github.com/spf13/cobra](https://github.com/spf13/cobra)
- **Table Rendering**: [github.com/olekukonko/tablewriter](https://github.com/olekukonko/tablewriter)
- **YAML Processing**: [github.com/goccy/go-yaml](https://github.com/goccy/go-yaml)

## Implementation Details
- The tool interacts with the local Copilot CLI server (started/managed by the SDK).
- It uses the user's existing login session (e.g., via `gh auth`).
- Most subcommands utilize a temporary session created via `client.CreateSession`.

## Instructions for Agents
- When modifying this project, maintain consistent table layouts using the `configureTable` helper.
- Ensure all output-related subcommands support both `table` and `yaml` formats via the `--format` flag.
- Keep "useful but cluttered" or internal-only information under `Hidden: true` subcommands.
- Follow Go best practices and ensure `go mod tidy` is run after adding dependencies.
- Never introduce new abbreviations in the code or UI without explicit user permission. (Exception: 'requests' may be abbreviated as 'req.' in table headers).
- Use user-facing terminology in table headers (e.g., 'Used', 'Billed' instead of 'Gross', 'Net' in usage reports). Use 'Included' instead of 'Entitlement' for premium request limits.
- All significant UI modifications (changes to table layouts, sorting, or new display formats) must support A/B testing.
- Implement a mechanism (e.g., using the `--ui-version` flag) to toggle between the old ("v1") and new ("v2") implementations.
- Keep both implementations temporarily to allow for side-by-side comparison and verification of the improvements.
- Document the changes and the results of the A/B test before removing the old implementation.
