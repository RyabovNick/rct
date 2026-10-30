package chat

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"slices"
	"time"

	"github.com/RyabovNick/rct/internal/cli"
	"github.com/RyabovNick/rct/internal/rocketchat"
)

// historyPage is the most a history request returns (API_Upper_Count_Limit).
const historyPage = 100

// messagesBefore returns up to n room messages before ts, oldest first, and
// whether older ones exist.
func (s *session) messagesBefore(r room, ts time.Time, n int) ([]rocketchat.Message, bool, error) {
	if n == 0 {
		return nil, false, nil
	}
	// Some fetched messages may be system messages, which are dropped.
	count := min(historyPage, n+10)
	msgs, raw, err := s.history(r, time.Time{}, ts, count, 0)
	if err != nil {
		return nil, false, err
	}
	more := raw == count || len(msgs) > n
	msgs = msgs[:min(n, len(msgs))]
	slices.Reverse(msgs)
	return msgs, more, nil
}

// messagesAfter returns up to n room messages after ts, oldest first, and
// whether newer ones exist. History answers newest first within a time
// window, so the window is narrowed until it holds less than a page and
// widened until it holds n messages or reaches the present.
func (s *session) messagesAfter(r room, ts time.Time, n int) ([]rocketchat.Message, bool, error) {
	if n == 0 {
		return nil, false, nil
	}
	now := time.Now()
	window := 6 * time.Hour
	var msgs []rocketchat.Message
	for range 10 {
		latest := ts.Add(window)
		reachedNow := !latest.Before(now)
		if reachedNow {
			latest = now.Add(time.Minute)
		}
		page, raw, err := s.history(r, ts, latest, historyPage, 0)
		if err != nil {
			return nil, false, err
		}
		msgs = page
		switch {
		case raw == historyPage && window > time.Minute:
			// The window holds more than a page, so its earliest messages are missing.
			window /= 4
			continue
		case len(msgs) >= n || reachedNow || raw == historyPage:
			slices.Reverse(msgs)
			more := len(msgs) > n || !reachedNow
			return msgs[:min(n, len(msgs))], more, nil
		}
		window *= 4
	}
	slices.Reverse(msgs)
	return msgs[:min(n, len(msgs))], true, nil
}

// threadMessages returns a thread's replies oldest first, up to limit, and the total.
func (s *session) threadMessages(tmid string, limit int) ([]rocketchat.Message, int, error) {
	var out []rocketchat.Message
	total := 0
	for len(out) < limit {
		count := min(historyPage, limit-len(out))
		params := url.Values{"tmid": {tmid}, "count": {fmt.Sprint(count)}, "offset": {fmt.Sprint(len(out))}, "sort": {`{"ts":1}`}}
		var res struct {
			Messages []json.RawMessage `json:"messages"`
			Total    int               `json:"total"`
		}
		if _, err := s.c.Get("/api/v1/chat.getThreadMessages", params, &res); err != nil {
			return nil, 0, err
		}
		msgs, err := rocketchat.DecodeMessages(res.Messages)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, msgs...)
		total = res.Total
		if len(res.Messages) < count || len(out) >= total {
			break
		}
	}
	slices.SortStableFunc(out, byTime)
	return out, total, nil
}

// maxThread caps how much of a thread context reads to find a reply.
const maxThread = 2000

// RunContext implements `rct context`.
func RunContext(args []string, stdout, stderr io.Writer) error {
	fs := cli.NewFlags("context", "<message-id|link>", "A message with the messages around it: in its room, or in its thread when it is a\n"+
		"reply. To read further, run context again on the first or last message shown.", stderr)
	before := fs.Int("before", 20, "messages before it, 0-100")
	after := fs.Int("after", 20, "messages after it, 0-100")
	asJSON := fs.Bool("json", false, "print the messages as returned by the API")
	pos, err := cli.ParseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return cli.Fail(cli.ExitUsage, "context takes one message id or link")
	}
	if err = checkRange("before", *before, 0, 100); err != nil {
		return err
	}
	if err = checkRange("after", *after, 0, 100); err != nil {
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
	m, err := s.message(id)
	if err != nil {
		return err
	}
	r, err := s.roomByID(m.RID)
	if err != nil {
		return err
	}

	var root *rocketchat.Message
	var prev, next []rocketchat.Message
	var morePrev, moreNext bool
	if m.TMID != "" {
		rm, rootErr := s.message(m.TMID)
		if rootErr != nil {
			return rootErr
		}
		root = &rm
		replies, _, threadErr := s.threadMessages(m.TMID, maxThread)
		if threadErr != nil {
			return threadErr
		}
		replies = visible(replies, true)
		i := slices.IndexFunc(replies, func(x rocketchat.Message) bool { return x.ID == m.ID })
		if i < 0 {
			// Past maxThread; show the message alone rather than the wrong neighbors.
			fmt.Fprintf(stderr, "rct: %s is beyond the first %d replies of its thread; showing it alone\n", m.ID, maxThread)
		} else {
			lo, hi := max(0, i-*before), min(len(replies), i+1+*after)
			prev, next = replies[lo:i], replies[i+1:hi]
			morePrev, moreNext = lo > 0, hi < len(replies)
		}
	} else {
		if prev, morePrev, err = s.messagesBefore(r, m.TS, *before); err != nil {
			return err
		}
		if next, moreNext, err = s.messagesAfter(r, m.TS, *after); err != nil {
			return err
		}
	}

	if *asJSON {
		out := map[string]any{
			"message": m.Raw, "before": rocketchat.RawMessages(prev), "after": rocketchat.RawMessages(next),
			"room": map[string]string{"id": r.ID, "name": r.label(), "type": r.kind()},
		}
		if root != nil {
			out["threadRoot"] = root.Raw
		}
		return cli.MarshalJSON(stdout, out)
	}

	link := s.c.Base + r.path() + "?msg=" + m.ID
	p := people{}
	p.add(m)
	p.add(prev...)
	p.add(next...)
	o := lineOpts{inThread: root != nil}
	if root != nil {
		p.add(*root)
		fmt.Fprintf(stdout, "# Thread %s in %s: around %s\n", root.ID, r.title(), m.ID)
	} else {
		fmt.Fprintf(stdout, "# %s: around %s\n", r.title(), m.ID)
	}
	fmt.Fprintf(stdout, "URL: %s\n", link)
	p.write(stdout)
	fmt.Fprintln(stdout)
	if root != nil {
		writeMessage(stdout, "Thread root:", root, lineOpts{maxChars: 400})
		if morePrev {
			fmt.Fprintln(stdout, "…")
		}
	}
	for i := range prev {
		writeMessage(stdout, "-", &prev[i], o)
	}
	writeMessage(stdout, "→", &m, o)
	for i := range next {
		writeMessage(stdout, "-", &next[i], o)
	}

	fmt.Fprintln(stdout)
	first, last := m.ID, m.ID
	if len(prev) > 0 {
		first = prev[0].ID
	}
	if len(next) > 0 {
		last = next[len(next)-1].ID
	}
	if morePrev {
		fmt.Fprintf(stdout, "earlier: rct context %s --before %d --after 0\n", first, max(*before, 20))
	}
	if moreNext {
		fmt.Fprintf(stdout, "later:   rct context %s --before 0 --after %d\n", last, max(*after, 20))
	}
	if root != nil {
		fmt.Fprintf(stdout, "thread:  rct thread %s\n", root.ID)
	} else if m.TCount > 0 {
		fmt.Fprintf(stdout, "thread:  rct thread %s (%d %s)\n", m.ID, m.TCount, plural(m.TCount, "reply", "replies"))
	}
	return nil
}
