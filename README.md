# copilot-claude-proxy

A small Go proxy that exposes GitHub Copilot as an **Anthropic-compatible API**.

If you already pay for GitHub Copilot, you can use it as the backend for
[Claude Code](https://docs.anthropic.com/en/docs/claude-code) instead of an
Anthropic API key. Run the proxy, point Claude Code at it, and everything
works as usual. Copilot understands the same API that Claude Code speaks, so
the proxy doesn't need to translate anything: it just logs you into GitHub
and passes your requests along.

## Quick start

Run it with `go run` (Go 1.25 or newer).

Authenticate with GitHub (device flow; opens your browser automatically):

```console
$ go run github.com/fabriziosestito/copilot-claude-proxy/cmd/copilot-claude-proxy@latest auth
```

Generate the Claude Code configuration (interactive model selection):

```console
$ go run github.com/fabriziosestito/copilot-claude-proxy/cmd/copilot-claude-proxy@latest setup
```

Start the proxy and Claude Code together:

```console
$ go run github.com/fabriziosestito/copilot-claude-proxy/cmd/copilot-claude-proxy@latest run
```

`run` shuts the proxy down when Claude Code exits. To keep the proxy up across
sessions, use `start` instead:

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
| `run`    | Run the proxy and Claude Code together, stopping the proxy when Claude Code exits  |
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

### `run` options

`run` takes every `start` option, waits until the proxy accepts connections,
then hands the terminal to `claude`. Claude Code's exit status becomes the exit
status of `run`, and Ctrl-C goes to Claude Code rather than tearing the proxy
down.

Arguments after `--` are forwarded to Claude Code:

```console
$ copilot-claude-proxy run -- --resume
```

Because the proxy shares the terminal with Claude Code's UI, its log output is
limited to errors. Use `--log-file/-l` to capture the full log instead:

| Flag              | Default | Description                                                    |
| ----------------- | ------- | -------------------------------------------------------------- |
| `--log-file, -l`  |         | Append proxy logs here (also `COPILOT_CLAUDE_PROXY_LOG_FILE`)  |
| `--no-statusline` |         | Leave the Claude Code status line alone                        |
| `--verbose, -v`   |         | Debug logging (pair with `--log-file` to keep the UI readable) |

#### How the session is pinned

`run` passes its configuration as a JSON document on `claude --settings`, which
Claude Code layers over `~/.claude/settings.json` and over the inherited
environment. That is what makes `--host`/`--port` effective: the address the
proxy actually bound to wins over whatever `setup` recorded, so running on
another port needs no second `setup`.

The document carries `ANTHROPIC_BASE_URL`, an `ANTHROPIC_AUTH_TOKEN` placeholder
(the proxy does not authenticate its clients), and — unless `--no-statusline` —
a status line row showing the tier, token TTL, and last resolved model. The
model selection stays in `~/.claude/settings.json`, so `run` still needs `setup`
to have picked the models.

Variables that would override or bypass those settings are removed from Claude
Code's environment, `ANTHROPIC_API_KEY` most importantly: Claude Code sends it
as an `x-api-key` header even when the auth token comes from `--settings`, which
would put a real Anthropic credential on every request to this proxy. The rest
name a different endpoint or select another provider (`CLAUDE_CODE_USE_BEDROCK`
and friends). Everything else you exported is passed through untouched.

A `--settings` of your own after `--` is merged into this document rather than
forwarded as a second flag, since Claude Code keeps only the last `--settings`
on a command line and forwarding yours would silently drop the proxy
connection. Your keys are kept except where they would route the session away
from the proxy.

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
| `GET /health`                    | Token/catalog health (503 when degraded, for monitors)                                        |
| `GET /status`                    | Session detail for the status line: tier, token TTL, last resolved model, counters (always 200) |

## Token storage

The GitHub OAuth token obtained by `auth` is stored in the operating system
keyring, and `logout` removes it. On headless systems without a keyring, pass
the token with `--github-token` or the `GH_TOKEN`/`GITHUB_TOKEN` environment
variables instead.

## License

[GPL-3.0](LICENSE)
