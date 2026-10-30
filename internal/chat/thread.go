package chat

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/RyabovNick/rct/internal/cli"
	"github.com/RyabovNick/rct/internal/rocketchat"
)

// RunThread implements `rct thread`.
func RunThread(args []string, stdout, stderr io.Writer) error {
	fs := cli.NewFlags("thread", "<message-id|link>", "A whole thread, oldest first: its root message and the replies.\n"+
		"Any message of the thread, root or reply, will do.", stderr)
	limit := fs.Int("limit", 200, "maximum replies")
	asJSON := fs.Bool("json", false, "print the messages as returned by the API")
	pos, err := cli.ParseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return cli.Fail(cli.ExitUsage, "thread takes one message id or link")
	}
	if err = checkRange("limit", *limit, 1, 5000); err != nil {
		return err
	}
	s, err := newSession(stderr)
	if err != nil {
		return err
	}
	id, err := s.messageID(pos[0])
	if err != nil {
		return err
	}
	root, err := s.message(id)
	if err != nil {
		return err
	}
	if root.TMID != "" {
		if root, err = s.message(root.TMID); err != nil {
			return err
		}
	}
	r, err := s.roomByID(root.RID)
	if err != nil {
		return err
	}
	var replies []rocketchat.Message
	total := 0
	if root.TCount > 0 {
		if replies, total, err = s.threadMessages(root.ID, *limit); err != nil {
			return err
		}
	}
	if *asJSON {
		return cli.MarshalJSON(stdout, map[string]any{"root": root.Raw, "replies": rocketchat.RawMessages(replies), "total": total})
	}
	replies = visible(replies, true)
	p := people{}
	p.add(root)
	p.add(replies...)
	fmt.Fprintf(stdout, "# Thread %s in %s\n", root.ID, r.title())
	fmt.Fprintf(stdout, "URL: %s%s?msg=%s\n", s.c.Base, r.path(), root.ID)
	p.write(stdout)
	fmt.Fprintln(stdout)
	writeMessage(stdout, "Root:", &root, lineOpts{})
	if len(replies) == 0 {
		fmt.Fprintln(stdout, "\nno replies; this message did not start a thread")
		return nil
	}
	fmt.Fprintf(stdout, "\n## Replies (%d)\n\n", total)
	for i := range replies {
		writeMessage(stdout, "-", &replies[i], lineOpts{inThread: true})
	}
	if total > len(replies) {
		last := replies[len(replies)-1].ID
		fmt.Fprintf(stdout, "\nshowing the first %d of %d; raise --limit, or rct context %s --before 0 --after 100\n", len(replies), total, last)
	}
	return nil
}

// RunThreads implements `rct threads`.
func RunThreads(args []string, stdout, stderr io.Writer) error {
	fs := cli.NewFlags("threads", "<ROOM>", "The threads in a room, most recent reply first, with their root message.", stderr)
	text := fs.String("text", "", "only threads whose root message contains this text")
	limit := fs.Int("limit", 20, "maximum threads")
	asJSON := fs.Bool("json", false, "print the raw API responses")
	pos, err := cli.ParseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return cli.Fail(cli.ExitUsage, "threads needs a room")
	}
	if err = checkRange("limit", *limit, 1, 1000); err != nil {
		return err
	}
	s, err := newSession(stderr)
	if err != nil {
		return err
	}
	r, err := s.resolveRoom(strings.Join(pos, " "))
	if err != nil {
		return err
	}
	var threads []rocketchat.Message
	var raws []json.RawMessage
	total := 0
	for len(threads) < *limit {
		count := min(historyPage, *limit-len(threads))
		params := url.Values{"rid": {r.ID}, "count": {fmt.Sprint(count)}, "offset": {fmt.Sprint(len(threads))}}
		if *text != "" {
			params.Set("text", *text)
		}
		var res struct {
			Threads []json.RawMessage `json:"threads"`
			Total   int               `json:"total"`
		}
		raw, err := s.c.Get("/api/v1/chat.getThreadsList", params, &res)
		if err != nil {
			return err
		}
		raws = append(raws, raw)
		msgs, err := rocketchat.DecodeMessages(res.Threads)
		if err != nil {
			return err
		}
		threads = append(threads, msgs...)
		total = res.Total
		if len(res.Threads) < count || len(threads) >= total {
			break
		}
	}
	if *asJSON {
		return cli.MarshalJSON(stdout, raws)
	}
	if len(threads) == 0 {
		fmt.Fprintf(stdout, "no threads in %s", r.title())
		if *text != "" {
			fmt.Fprintf(stdout, " with %q", *text)
		}
		fmt.Fprintln(stdout)
		return nil
	}
	p := people{}
	p.add(threads...)
	fmt.Fprintf(stdout, "# Threads in %s (%d of %d, most recent reply first)\n", r.title(), len(threads), total)
	p.write(stdout)
	fmt.Fprintln(stdout)
	for i := range threads {
		writeMessage(stdout, "-", &threads[i], lineOpts{maxChars: 200, lastReply: true})
	}
	if total > len(threads) {
		fmt.Fprintf(stdout, "\nshowing %d of %d; raise --limit or filter with --text\n", len(threads), total)
	}
	fmt.Fprintln(stdout, "\nrct thread <id> reads one.")
	return nil
}
