# AGENTS.md

Guidance for AI coding agents working in this repository.

## What this is

A small Go proxy that exposes GitHub Copilot as an Anthropic-compatible API,
so Claude Code can use a Copilot subscription as its backend. Copilot already
speaks the Anthropic Messages API, so the proxy does no translation: it
handles GitHub authentication (device flow, token in the OS keyring) and
forwards requests.

## Layout

- `cmd/copilot-claude-proxy/` — CLI entrypoint (urfave/cli/v3): `auth`,
  `logout`, `start`, `setup`, `models` commands.
- `internal/server/` — the Anthropic-compatible HTTP surface
  (`/v1/messages`, streaming, token counting, `/health`).
- `internal/copilot/` — Copilot API client, token exchange/refresh, model
  catalog, account-tier probing.
- `internal/auth/` — GitHub device-flow login.
- `internal/storage/` — OS keyring storage for the GitHub token.
- `internal/claudecode/` + `internal/setup/` — `setup` command: generates
  Claude Code settings pointing at the proxy.

## Build, test, lint

```sh
make build   # go build -> bin/copilot-claude-proxy
make test    # go vet, then go test -race ./...
make lint    # golangci-lint (auto-installed into bin/ at the pinned version)
make all     # build + test + lint — run this before considering work done
```

CI runs `make test` and golangci-lint at the version pinned in the Makefile
(`GOLANGCI_LINT_VERSION`); keep the two in sync if you bump it.

## Conventions

- Go 1.25+. Dependencies are deliberately minimal (stdlib + `urfave/cli/v3` +
  `zalando/go-keyring`); prefer the standard library over adding a module.
  `depguard` enforces an allowlist.
- Linting uses a strict golangci-lint "golden config" (`.golangci.yml`).
  Notable rules you will hit:
  - No global variables (`gochecknoglobals`) and no `init` functions
    (`gochecknoinits`).
  - Structured logging only, via `log/slog` with a context
    (`sloglint`: no global logger, context-aware calls).
  - Comments end with a period (`godot`); exported identifiers are documented.
  - `nolint` directives must name the linter and include an explanation.
  - Shadowing of `err`/`ok` is already excluded — don't rename variables to
    appease `govet` shadow warnings.
- Tests live in separate `_test` packages (`testpackage`), use `t.Parallel()`
  (`paralleltest`), and run with `-race`. Table-driven tests are the norm.
- Line length ≤ 120 (`golines`); imports grouped with the module path as the
  local prefix (`goimports`).

## Commits

When asked to commit, this is what is meant:

- Conventional Commits, matching the existing history (`feat:`, `fix:`,
  `chore:`, `docs:`, ...).
- Subject only — one sentence, no body paragraphs.
- Sign off (`git commit -s`).
- Do not add an AI co-author trailer, a "Generated with ..." footer, or any
  other AI attribution — in commits, PR titles/bodies, or anywhere else.
- Split into several commits when the changes are logically distinct.

## Gotchas

- Never log or print the GitHub/Copilot tokens; they live in the OS keyring
  and are treated as secrets throughout (see SECURITY.md).
- The proxy intentionally does not translate request/response bodies —
  resist adding transformation layers between Claude Code and Copilot.
- `internal/` packages are not importable by other modules; there is no
  public Go API surface to preserve, but CLI flags and the README tables
  document user-facing behavior — update the README when changing them.
