package main

import (
	"fmt"
	"io"
	"os"

	"github.com/RyabovNick/rct/internal/chat"
	"github.com/RyabovNick/rct/internal/cli"
	"github.com/RyabovNick/rct/internal/doctor"
	"github.com/RyabovNick/rct/internal/install"
)

const usage = `rct: read-only Rocket.Chat lookups

  rct rooms [TEXT] [--type LIST] [--json]    your channels, groups, discussions and DMs:
                                              name, display name (a DM partner's full
                                              name), last activity, id; most active first
  rct rooms --public [TEXT] [--limit N]      public channels, joined or not
  rct users [TEXT] [--limit N] [--json]      users: username, full name, email if shown
  rct search <text> (--room ROOM... | --all [--max-rooms N]) [--since DATE|7d]
             [--exact] [--limit N] [--json]  messages, thread replies included, per room;
                                              --exact matches substrings such as PROJ-123
  rct context <MSG> [--before N] [--after N] [--json]
                                              a message and the messages around it, in
                                              its room or, for a reply, its thread
  rct thread <MSG> [--limit N] [--json]      a thread's root and replies, oldest first
  rct threads <ROOM> [--text T] [--limit N] [--json]
                                              a room's threads, latest reply first
  rct history <ROOM> [--since DATE|7d] [--limit N] [--json]
                                              a room's latest messages, oldest first
  rct doctor [--json]                         where each setting comes from, and
                                              whether Rocket.Chat accepts the token
  rct install [--agents LIST] [--bin-dir DIR] [--no-binary] [--no-instructions] [--force]
                                              copy the binary, write the skill and the
                                              instructions section for Claude Code, Codex
                                              and/or opencode; asks which without --agents,
                                              with opencode selected
  rct uninstall [--bin-dir DIR] [--keep-binary]
  rct version

ROOM is a #name, @username, display name, room id or link; MSG a message id or link.
Credentials: ROCKETCHAT_URL, ROCKETCHAT_USER_ID, ROCKETCHAT_TOKEN (a personal access
token), over $RCT_CONFIG or ~/.config/rct/config.ini.
Exit codes: 0 ok, 1 API or network, 2 usage or rejected request, 3 config, 4 not found.
`

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return cli.ExitUsage
	}
	commands := map[string]func([]string, io.Writer, io.Writer) error{
		"rooms":     chat.RunRooms,
		"users":     chat.RunUsers,
		"search":    chat.RunSearch,
		"context":   chat.RunContext,
		"thread":    chat.RunThread,
		"threads":   chat.RunThreads,
		"history":   chat.RunHistory,
		"doctor":    doctor.Run,
		"install":   install.Run,
		"uninstall": install.RunUninstall,
	}
	cmd, ok := commands[args[0]]
	if !ok {
		switch args[0] {
		case "-h", "--help", "help":
			fmt.Fprint(stdout, usage)
			return 0
		case "version", "--version":
			fmt.Fprintf(stdout, "rct %s\n", cli.Version)
			return 0
		}
		fmt.Fprintf(stderr, "rct: unknown command %q\n\n%s", args[0], usage)
		return cli.ExitUsage
	}
	return cli.Report(cmd(args[1:], stdout, stderr), stderr)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
