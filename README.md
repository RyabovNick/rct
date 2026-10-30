# rct

Read-only Rocket.Chat (REST API, as of 8.5) lookups for coding agents, plus a
skill for Claude Code, Codex and opencode that tells the agent when to use
them: find a room by name, search messages and thread replies, read the
conversation around a hit, and map usernames to full names for Jira and
Confluence.

## Install

Each way ends in `rct install`. It asks which agents to set up (space to
select, enter to confirm), with opencode selected; without a terminal it sets
up opencode alone. `--agents claude,codex,opencode` or `--agents all` skips the
question. It copies the binary to `~/.local/bin` (`--bin-dir` to change),
writes the `rocketchat` skill, and adds a short section to each agent's global
instructions telling it to look in chat when the user mentions one:

| Agent | Skill | Instructions |
| --- | --- | --- |
| Claude Code | `~/.claude/skills/rocketchat/` | `~/.claude/CLAUDE.md` |
| Codex | `~/.agents/skills/rocketchat/` | `~/.codex/AGENTS.md` |
| opencode | `~/.agents/skills/rocketchat/` | `~/.config/opencode/AGENTS.md` |

`CLAUDE_CONFIG_DIR`, `CODEX_HOME` and `OPENCODE_CONFIG_DIR` move those
directories. The section sits between `<!-- rct:begin -->` and
`<!-- rct:end -->`; rerunning updates it in place and leaves the rest of the
file alone.

- **Linux or macOS, no Go:** run [install.sh](install.sh). It downloads the
  latest release and checks it against `SHA256SUMS`. `RCT_VERSION` picks a
  release instead of the latest, and other arguments go to `rct install`.

  ```bash
  curl -fsSL https://raw.githubusercontent.com/RyabovNick/rct/main/install.sh | sh
  ```
- **A release binary, any system:** download `rct-<os>-<arch>` from
  [Releases](https://github.com/RyabovNick/rct/releases), check it against `SHA256SUMS`, and run
  `./rct-<os>-<arch> install`. On Windows, download `rct-windows-amd64.exe`,
  rename it to `rct.exe` and run `rct.exe install`; it installs to
  `%LOCALAPPDATA%\Programs\rct`.
- **With Go:** `go install github.com/RyabovNick/rct@latest`, then
  `rct install`. From a clone, `make install` runs the tests, builds, and
  runs `./rct install`.

Then create a personal access token (avatar → Account → Personal Access
Tokens; tick "Ignore Two Factor Authentication" if you use 2FA), note the user
ID shown next to it, set them, check them, and restart your agents:

```bash
export ROCKETCHAT_URL=https://chat.example.com ROCKETCHAT_USER_ID=... ROCKETCHAT_TOKEN=...
# or: install -m 600 config.example.ini ~/.config/rct/config.ini and edit it
rct doctor            # where each setting comes from; does the server accept the token
```

`rct uninstall` (or `make uninstall`) removes the binary, the skills and the
instructions sections for every agent, and leaves the config file in place.
`rct version` prints the build's version.

## Commands

`ROOM` is a `#name`, an `@username` (your DM with them), a display name, a
room id or a room link; `MSG` is a message id (the `[id]` on every message
line) or a message link. Every command below takes `--json`: the messages or
responses as the API returned them, or for `doctor` its own report.

| Command | Returns |
| --- | --- |
| `rct rooms [TEXT] [--type LIST]` | every room you are in, grouped into channels, private groups, discussions and direct messages, most recently active first: name, display name (a DM partner's full name, e.g. `@walkerjj — Walker John Johnson`), members, last activity, unread count, id and topic; `TEXT` filters by name or display name, `--type` by `channel,group,discussion,dm` |
| `rct rooms --public [TEXT] [--limit 50]` | public channels from the directory, marking the ones you have not joined |
| `rct users [TEXT] [--limit 50]` | users from the directory: username, full name, nickname, and email where the server shows it |
| `rct search <text> --room ROOM... [--limit 20]` | matching messages, thread replies included, per room, newest first; `--room` repeats |
| `rct search <text> --all [--max-rooms 10]` | the same over your most recently active rooms, skipping rooms that fail |
| `rct search … [--since 2026-10-01\|7d] [--words]` | only messages since a date (with `--all`, rooms quiet since then are skipped); `--words` uses the server's whole-word search |
| `rct context <MSG> [--before 20] [--after 20]` | the message with the messages around it, in its room, or within its thread for a reply; prints the commands that read further |
| `rct thread <MSG> [--limit 200]` | a thread's root and replies, oldest first, from the root or any reply |
| `rct threads <ROOM> [--text T] [--limit 20]` | a room's threads, most recent reply first, with reply counts |
| `rct history <ROOM> [--since 2d] [--limit 50]` | a room's latest messages, oldest first, thread roots with their reply counts |
| `rct doctor` | each setting and its source, then a login check and the server version |

Exit codes: `0` ok, `1` API or network, `2` usage or a request Rocket.Chat
rejected (the server's message is shown; an ambiguous room name lists the
candidates), `3` config, `4` not found or not visible to you. `rct doctor`
exits `0` when the server accepts the token, `1` when the check fails, and `3`
when nothing could be checked (nothing configured, or the config file is
unusable).

## Output

- **Messages.** One line each: time (local), username, `[id]`, notes such as
  `thread: 3 replies`, `reply in thread <root>`, `edited` or `pinned`, then the
  text. Quotes of other messages show as `> author: text`, uploads as
  `[file: name, type, id]`, integration cards as `[attachment: title] text`.
  System messages (joins, renames) are left out. Search cuts a message at 400
  characters and says how to read the rest.
- **People.** Listings start with `People: walkerjj = Walker John Johnson; …`
  for every author shown, so lines stay short and agents can still match chat
  users to Jira and Confluence.
- **Search.** The text becomes a case-insensitive regular expression, so it
  matches as a substring: `PROJ-123`, `ops/rollback.sh` or a phrase work as
  typed, and several words must appear together. `/regex/i` is passed through.
  Rocket.Chat's operators stay operators: `from:user`, `mention:user`,
  `has:url`, `is:pinned`, `has:star`, `before:`/`after:`/`on:DD-MM-YYYY`,
  `order:asc`. `--words` hands the words to the server's full-text index
  instead: whole words, any of them, with stemming.
- **Rooms.** A name resolves by exact id, then exact name or display name,
  then a unique prefix, then a unique substring; `@` prefers direct messages
  and `#` the others. More than one match exits 2 with the candidates. A public
  channel you have not joined is found by its exact name.
- **Context.** Room context leaves thread replies in their threads (unless
  they were also sent to the room); a reply's context is its thread, under the
  thread's root. History answers newest first within a time window, so rct
  narrows and widens the window to find the messages right after the target.

## Rate limits

Rocket.Chat rate-limits every REST endpoint, by default to 10 calls a minute
per user (Admin → Rate Limiter; users with the `api-bypass-rate-limit`
permission are exempt). On HTTP 429 rct waits until `X-RateLimit-Reset`, says
so on stderr, and retries up to three times; a wait longer than
`rate_limit_wait` (60 seconds by default) fails with exit 1 instead. `search
--all` costs one call per room, so it searches only the `--max-rooms` most
recently active rooms.

## Configuration and auth

`ROCKETCHAT_URL`, `ROCKETCHAT_USER_ID` and `ROCKETCHAT_TOKEN` override
`$RCT_CONFIG` or `~/.config/rct/config.ini`. The file is optional when the
environment supplies all three; see [config.example.ini](config.example.ini)
for the other settings (`ca_bundle`, `timeout`, `rate_limit_wait`). The token
and user ID are sent as the `X-Auth-Token` and `X-User-Id` headers, so rct
sees exactly what the token's owner sees.

## Security properties

- No write verbs: rct only sends GET requests and cannot change anything in
  Rocket.Chat.
- The token goes only to the configured host. Redirects are reported, not
  followed, and links to another server are refused.
- rct refuses a config file other users can read. `rct doctor` reports the
  token only as set or unset.

## Testing

`go test ./...` runs against a fake Rocket.Chat that follows the 8.5 API
(history windows, thread visibility per room type, search operators, rate
limits); no server or token is needed. `go test -cover ./...` reports coverage.

`make lint` runs [golangci-lint](https://golangci-lint.run) v2 with the
linters in [.golangci.yml](.golangci.yml); CI runs the same version, plus
the race detector and a `go mod tidy` check.

## License

[MIT](LICENSE)
