package chat

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/RyabovNick/rct/internal/cli"
	"github.com/RyabovNick/rct/internal/rocketchat"
)

// RunHistory implements `rct history`.
func RunHistory(args []string, stdout, stderr io.Writer) error {
	fs := cli.NewFlags("history", "<ROOM>", "The latest messages in a room, oldest first. Thread replies stay in their threads;\n"+
		"thread roots show their reply count.", stderr)
	sinceFlag := fs.String("since", "", "only messages since a date (2026-10-01) or duration (7d, 48h)")
	limit := fs.Int("limit", 50, "maximum messages")
	asJSON := fs.Bool("json", false, "print the messages as returned by the API")
	pos, err := cli.ParseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return cli.Fail(cli.ExitUsage, "history needs a room")
	}
	if err = checkRange("limit", *limit, 1, 1000); err != nil {
		return err
	}
	var since time.Time
	if *sinceFlag != "" {
		if since, err = cli.ParseSince(*sinceFlag, time.Now()); err != nil {
			return err
		}
	}
	s, err := newSession(stderr)
	if err != nil {
		return err
	}
	r, err := s.resolveRoom(strings.Join(pos, " "))
	if err != nil {
		return err
	}
	var msgs []rocketchat.Message
	more := false
	for offset := 0; ; {
		count := min(historyPage, *limit-len(msgs)+10)
		page, raw, err := s.history(r, since, time.Time{}, count, offset)
		if err != nil {
			return err
		}
		msgs = append(msgs, page...)
		offset += raw
		if len(msgs) >= *limit {
			more = len(msgs) > *limit || raw == count
			msgs = msgs[:*limit]
			break
		}
		if raw < count {
			break
		}
	}
	slices.Reverse(msgs)
	if *asJSON {
		return cli.MarshalJSON(stdout, map[string]any{
			"room":     map[string]string{"id": r.ID, "name": r.label(), "type": r.kind()},
			"messages": rocketchat.RawMessages(msgs),
		})
	}
	if len(msgs) == 0 {
		fmt.Fprintf(stdout, "no messages in %s", r.title())
		if !since.IsZero() {
			fmt.Fprintf(stdout, " since %s", since.Format("2006-01-02 15:04"))
		}
		fmt.Fprintln(stdout)
		return nil
	}
	p := people{}
	p.add(msgs...)
	fmt.Fprintf(stdout, "# %s: %d %s", r.title(), len(msgs), plural(len(msgs), "message", "messages"))
	if !since.IsZero() {
		fmt.Fprintf(stdout, " since %s", since.Format("2006-01-02 15:04"))
	}
	fmt.Fprintf(stdout, "\nURL: %s%s\n", s.c.Base, r.path())
	p.write(stdout)
	fmt.Fprintln(stdout)
	for i := range msgs {
		writeMessage(stdout, "-", &msgs[i], lineOpts{})
	}
	if more {
		fmt.Fprintf(stdout, "\nolder: rct context %s --before 50 --after 0\n", msgs[0].ID)
	}
	return nil
}
