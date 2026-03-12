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

## Usage

Run the tool with a subcommand to inspect specific resources.

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

### YAML Output

All commands support `-f yaml` flag to output detailed data in YAML format.

```bash
copilot-show quota -f yaml
```

## Hidden Subcommands

The following commands are hidden by default but can be executed by specifying their names:

- `agents`: List available Copilot agents
- `current-model`: Show the currently selected model ID
- `current-agent`: Show the currently selected agent
- `mode`: Show the current agent mode
- `plan`: Read the current plan file
- `workspace`: List files in the workspace
- `read-file <path>`: Read a specific file from the workspace
- `ping`: Check connection to the server
- `status`: Show CLI version and authentication status
- `sessions`: List all sessions with PID information
- `history [sessionID]`: Show conversation history (from local event logs)

## License

MIT License
