# copilot-claude-proxy

A small Go proxy that exposes GitHub Copilot as an **Anthropic-compatible API**.

If you already pay for GitHub Copilot, you can use it as the backend for
[Claude Code](https://docs.anthropic.com/en/docs/claude-code) instead of an
Anthropic API key. Run the proxy, point Claude Code at it, and everything
works as usual. Copilot understands the same API that Claude Code speaks, so
the proxy doesn't need to translate anything: it just logs you into GitHub
and passes your requests along.

## Installation

### Homebrew (macOS and Linux)

```console
$ brew tap fabriziosestito/copilot-claude-proxy https://github.com/fabriziosestito/copilot-claude-proxy
$ brew install copilot-claude-proxy
```

### Pre-built binaries

Download the archive for your OS/architecture (including Windows) from the
[GitHub Releases page](https://github.com/fabriziosestito/copilot-claude-proxy/releases).

### `go run`

No installation needed; requires Go 1.25 or newer. Used throughout this README.

## Quick start

Run it with `go run` (Go 1.25 or newer), or use the `copilot-claude-proxy`
pre-built binary.

Authenticate with GitHub (device flow; opens your browser automatically):

```console
$ go run github.com/fabriziosestito/copilot-claude-proxy/cmd/copilot-claude-proxy@latest auth
```

Generate the Claude Code configuration (interactive model selection):

```console
$ go run github.com/fabriziosestito/copilot-claude-proxy/cmd/copilot-claude-proxy@latest setup
```

Run the proxy:

```console
$ go run github.com/fabriziosestito/copilot-claude-proxy/cmd/copilot-claude-proxy@latest start
```

Then, in another terminal:

```console
$ claude
```

## Commands

| Command  | Description                                                                        |
| -------- | ---------------------------------------------------------------------------------- |
| `auth`   | GitHub device-flow login; opens the browser and stores the token in the OS keyring |
| `logout` | Remove the stored token from the OS keyring                                        |
| `start`  | Run the proxy server (default `127.0.0.1:4141`)                                    |
| `setup`  | Generate Claude Code configuration pointing at this proxy                          |
| `models` | List the models available on your Copilot account                                  |

### `start` options

| Flag                 | Default     | Description                                                                          |
| -------------------- | ----------- | ------------------------------------------------------------------------------------ |
| `--port, -p`         | `4141`      | Listen port                                                                          |
| `--host, -H`         | `127.0.0.1` | Bind host                                                                            |
| `--account-type, -a` | `auto`      | Copilot tier: `auto`, `individual`, `business`, `enterprise` (auto probes your plan) |
| `--github-token, -g` |             | Token override (also `GH_TOKEN` / `GITHUB_TOKEN` env)                                |
| `--model-map`        |             | Extra aliases, repeatable: `--model-map haiku=claude-haiku-4.5`                      |
| `--verbose, -v`      |             | Debug logging                                                                        |

### `setup` options

`--model/-m` and `--small-model/-s` skip the interactive selection (both or
neither). `--with-extras/-e` also writes opinionated tuning vars (telemetry off,
auto-compact window, and `CLAUDE_CODE_ATTRIBUTION_HEADER=0`, which fixes prompt
caching through Copilot). `--yes/-y` skips the overwrite confirmation.

## Known issues

### `claude-fable-5` occasionally switches to Chinese

`claude-fable-5` has a known bug where it can start responding (and thinking)
in Chinese mid-session. The fix is to pin the language in `~/.claude/CLAUDE.md`
so Claude Code injects it into every session:

```markdown
# User preferences

- Always respond and display your thinking in English, regardless of the
  language used in project files, documentation, or code comments.
```

`setup` prints a reminder about this whenever a fable-5 model is selected.

## Building from source

```console
$ git clone https://github.com/fabriziosestito/copilot-claude-proxy
$ cd copilot-claude-proxy
$ make build
```

## Endpoints

| Route                            | Behavior                                                                                      |
| -------------------------------- | --------------------------------------------------------------------------------------------- |
| `POST /v1/messages`              | Enriched passthrough to Copilot's native Anthropic Messages API (streaming and non-streaming) |
| `POST /v1/messages/count_tokens` | Local estimation (~4 chars/token, thinking blocks excluded)                                   |
| `GET /v1/models`                 | Anthropic-format list of usable models                                                        |
| `POST /api/event_logging`        | Swallows Anthropic SDK telemetry                                                              |
| `GET /health`                    | Token/catalog health                                                                          |

## Token storage

The GitHub OAuth token obtained by `auth` is stored in the operating system
keyring, and `logout` removes it. On headless systems without a keyring, pass
the token with `--github-token` or the `GH_TOKEN`/`GITHUB_TOKEN` environment
variables instead.

## License

[GPL-3.0](LICENSE)
