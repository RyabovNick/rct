# AGENTS.md

rct is a read-only CLI that lets coding agents look up Rocket.Chat (REST API,
as of 8.5): rooms, users, message search, the context around a message,
threads and room history. `rct install` also sets up the `rocketchat` skill for
Claude Code, Codex and opencode. User-facing docs are in [README.md](README.md).

## Commands

```bash
make build        # ./rct, with the version from git describe
make test         # go vet + go test ./...
make lint         # golangci-lint v2 with .golangci.yml
go test -race ./...
go mod tidy -diff # must print nothing
```

CI (`.github/workflows/ci.yml`) runs the tidy check, `go test -race` and
golangci-lint v2.14.0. Run all of them before committing. The tests need no
network, Rocket.Chat server or token.

## Layout

- `main.go`: the usage text and the command table, nothing else.
- `skill/`: the agent-facing text that `rct install` writes, embedded into
  the binary. `rocketchat/SKILL.md` is the skill, and `agents-section.md` is
  the section added to CLAUDE.md or AGENTS.md.
- `internal/cli`: the `Error` type that carries an exit code (`Fail`,
  `Report`, `Code`), flag parsing, JSON output and `--since` parsing.
- `internal/rocketchat`: config (environment over `~/.config/rct/config.ini`),
  the HTTP client with its rate-limit retries, and the API types.
- `internal/chat`: the message commands (`rooms`, `users`, `search`,
  `context`, `thread`, `threads`, `history`), room and message resolution, and
  message rendering.
- `internal/doctor`, `internal/install`: one package per command group. Each
  command is a `Run…(args []string, stdout, stderr io.Writer) error` function
  registered in `main.go`.
- `internal/testenv`: the fake Rocket.Chat server and env helpers shared by
  the tests.

Keep this split. Put code in the package that owns the concept, and do not add
interfaces, wrappers or new packages without a real second user.

## Rules

- **Read-only.** rct only sends GET requests. Never add a request that posts,
  edits, reacts, marks as read or changes anything in Rocket.Chat, even
  through `method.call`.
- **Tokens stay safe.** The token goes only to the configured host: redirects
  are reported, not followed, and links to other hosts are refused. Never
  print the token; `rct doctor` reports it only as set or unset. The config
  file must stay mode 600, and rct refuses it otherwise.
- **Rocket.Chat quirks.** Missing or inaccessible rooms and messages come back
  as HTTP 400 (`not-allowed`, `error-room-not-found`, or a bare failure from
  `chat.getMessage`); map them to `ExitNotFound`. `groups.history` returns
  thread replies unless `showThreadMessages=false`, unlike `channels.history`
  and `im.history`, so always send it. Every endpoint is rate-limited (10
  calls a minute by default): keep the number of requests per command small,
  and keep the client's 429 handling.
- **Errors and exit codes.** Return `cli.Fail(code, …)` with `ExitAPI` (1),
  `ExitUsage` (2), `ExitConfig` (3) or `ExitNotFound` (4); main prints the
  message once. Keep these codes stable: the skill tells agents what each one
  means.
- **Output is for agents.** Commands print compact Markdown, and `--json`
  prints the API's messages or responses. Keep output short and stable, since
  the skill and agents depend on its shape (`[id]` on message lines, the
  `People:` line, `earlier:`/`later:` hints, `## <room>` headings).
- **Installer.** rct owns only the text between `<!-- rct:begin -->` and
  `<!-- rct:end -->` in an instructions file. It must leave the rest of the
  user's file untouched and stay idempotent: a rerun changes nothing.
- **Dependencies.** Stay on the standard library and `golang.org/x/*`. Ask
  before adding a module.

## When changing behavior

- **Commands and flags:** update the usage text in `main.go`, the README's
  Commands table, and `skill/rocketchat/SKILL.md` if agents should use the
  change.
- **Config and env vars:** update `config.example.ini` and the README.
- **Tests:** add one next to the code, in the same package. Command tests run
  against `testenv.FakeServer`; extend it when you need new endpoints rather
  than mocking the client, and follow the real server's behavior.

## Style

- Go 1.27. Use modern idioms (the `modernize` linter enforces them), US
  spelling, and `%w` when wrapping errors.
- Comment the why, briefly, and match the existing density. Exported
  identifiers get a one-line doc comment.
- Commit messages are Conventional Commits (`feat:`, `fix:`, `refactor:`,
  `docs:`, `ci:`, `deps:`, `chore:`), optionally scoped as in `feat(search):`,
  with a body explaining why.
