package chat

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/RyabovNick/rct/internal/cli"
	"github.com/RyabovNick/rct/internal/rocketchat"
)

// operatorRE matches the search operators Rocket.Chat parses out of the text
// before the word search: from:me, mention:x, has:url, is:pinned, before:/
// after:/on:DD-MM-YYYY, order:asc, label:, file-desc:, file-title:.
var operatorRE = regexp.MustCompile(`^(from|mention|has|is|before|after|on|order|sort|label|file-desc|file-title):\S+$`)

// regexTextRE matches text that is already a /regex/flags search.
var regexTextRE = regexp.MustCompile(`^/.+/[imxs]*$`)

// searchText builds chat.search's searchText. By default the words become a
// case-insensitive regular expression, so the text matches as a substring
// (PROJ-123, ops/rollback.sh); --words leaves them to the server's full-text
// search, which matches whole words, any of them, with stemming. --since adds
// an after: operator two days early, since it has day precision in the user's
// time zone; results are filtered exactly afterwards.
func searchText(text string, words bool, since time.Time) string {
	var ops, free []string
	for f := range strings.FieldsSeq(text) {
		if operatorRE.MatchString(f) {
			ops = append(ops, f)
		} else {
			free = append(free, f)
		}
	}
	if !since.IsZero() {
		ops = append(ops, "after:"+since.AddDate(0, 0, -2).Format("02-01-2006"))
	}
	phrase := strings.Join(free, " ")
	if !words && phrase != "" && !regexTextRE.MatchString(phrase) {
		// The server splits /…/flags on "/", so a slash in the text is written as \x2F.
		phrase = "/" + strings.ReplaceAll(regexp.QuoteMeta(phrase), "/", `\x2F`) + "/i"
	}
	return strings.TrimSpace(strings.Join(append(ops, phrase), " "))
}

type roomResult struct {
	room     room
	messages []rocketchat.Message
	more     bool
}

func (s *session) search(r room, query string, since time.Time, limit int) (roomResult, error) {
	res := roomResult{room: r}
	for offset := 0; len(res.messages) < limit; {
		count := min(100, limit-len(res.messages))
		params := url.Values{"roomId": {r.ID}, "searchText": {query}, "count": {fmt.Sprint(count)}, "offset": {fmt.Sprint(offset)}}
		var page struct {
			Messages []json.RawMessage `json:"messages"`
		}
		if _, err := s.c.Get("/api/v1/chat.search", params, &page); err != nil {
			return res, err
		}
		msgs, err := rocketchat.DecodeMessages(page.Messages)
		if err != nil {
			return res, err
		}
		for _, m := range msgs {
			if m.T == "" && (since.IsZero() || !m.TS.Before(since)) {
				res.messages = append(res.messages, m)
			}
		}
		offset += len(page.Messages)
		if len(page.Messages) < count {
			return res, nil
		}
	}
	res.more = true
	return res, nil
}

// RunSearch implements `rct search`.
func RunSearch(args []string, stdout, stderr io.Writer) error {
	fs := cli.NewFlags("search", "<text> (--room ROOM... | --all)", "Search messages, thread replies included, in the given rooms or all of yours.\n"+
		"The text matches case-insensitively as a substring (PROJ-123, \"rollback script\");\n"+
		"/regex/i is passed through, and --words uses the server's whole-word search instead.\n"+
		"Operators go in the text: from:walkerjj mention:me has:url is:pinned on:28-10-2026.", stderr)
	var rooms cli.Strings
	fs.Var(&rooms, "room", "a room to search: #name, @username, display name, id or link; repeatable")
	all := fs.Bool("all", false, "search every room you are in, most recently active first")
	maxRooms := fs.Int("max-rooms", 10, "with --all, how many rooms to search (Rocket.Chat rate-limits each endpoint)")
	sinceFlag := fs.String("since", "", "only messages since a date (2026-10-01) or duration (7d, 48h); with --all, skips quieter rooms")
	words := fs.Bool("words", false, "use the server's full-text search: whole words, any of them, with stemming")
	limit := fs.Int("limit", 20, "maximum messages per room")
	asJSON := fs.Bool("json", false, "print the matching messages as returned by the API, per room")
	pos, err := cli.ParseFlags(fs, args)
	if err != nil {
		return err
	}
	text := strings.Join(pos, " ")
	switch {
	case strings.TrimSpace(text) == "":
		return cli.Fail(cli.ExitUsage, "search needs text to look for")
	case len(rooms) == 0 && !*all:
		return cli.Fail(cli.ExitUsage, "search needs --room ROOM (repeatable) or --all; rct rooms lists your rooms")
	case len(rooms) > 0 && *all:
		return cli.Fail(cli.ExitUsage, "pass --room or --all, not both")
	}
	if err = checkRange("limit", *limit, 1, 1000); err != nil {
		return err
	}
	if err = checkRange("max-rooms", *maxRooms, 1, 500); err != nil {
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
	targets, err := searchTargets(s, rooms, *all, since, *maxRooms)
	if err != nil {
		return err
	}
	query := searchText(text, *words, since)

	var results []roomResult
	var empty, failed []string
	for _, r := range targets {
		res, err := s.search(r, query, since, *limit)
		if err != nil {
			// One unreadable room should not lose the others' results under --all.
			if !*all || cli.Code(err) == cli.ExitConfig {
				return err
			}
			fmt.Fprintf(stderr, "rct: skipped %s: %v\n", r.label(), err)
			failed = append(failed, r.label())
			continue
		}
		if len(res.messages) == 0 {
			empty = append(empty, r.label())
			continue
		}
		results = append(results, res)
	}
	if *asJSON {
		type jsonRoom struct {
			ID       string            `json:"id"`
			Name     string            `json:"name"`
			Messages []json.RawMessage `json:"messages"`
		}
		out := struct {
			SearchText string     `json:"searchText"`
			Rooms      []jsonRoom `json:"rooms"`
		}{SearchText: query, Rooms: []jsonRoom{}}
		for _, res := range results {
			out.Rooms = append(out.Rooms, jsonRoom{res.room.ID, res.room.label(), rocketchat.RawMessages(res.messages)})
		}
		return cli.MarshalJSON(stdout, out)
	}
	return writeSearch(stdout, query, *words, targets, results, empty, failed)
}

func searchTargets(s *session, names []string, all bool, since time.Time, maxRooms int) ([]room, error) {
	if !all {
		var out []room
		seen := map[string]bool{}
		for _, n := range names {
			r, err := s.resolveRoom(n)
			if err != nil {
				return nil, err
			}
			if !seen[r.ID] {
				seen[r.ID] = true
				out = append(out, r)
			}
		}
		return out, nil
	}
	rooms, err := s.memberRooms()
	if err != nil {
		return nil, err
	}
	var out []room
	quiet := 0
	for _, r := range rooms {
		if r.T == "l" {
			continue
		}
		if !since.IsZero() && !r.LastActivity.IsZero() && r.LastActivity.Before(since) {
			quiet++
			continue
		}
		out = append(out, r)
	}
	active := len(out)
	if len(out) > maxRooms {
		out = out[:maxRooms]
	}
	note := fmt.Sprintf("rct: searching %d of %d rooms, most recently active first", len(out), len(rooms))
	if quiet > 0 {
		note += fmt.Sprintf("; %d had no messages since %s", quiet, since.Format("2006-01-02"))
	}
	if active > len(out) {
		note += fmt.Sprintf("; raise --max-rooms (%d more) or name rooms with --room", active-len(out))
	}
	fmt.Fprintln(s.stderr, note)
	return out, nil
}

func writeSearch(w io.Writer, query string, words bool, targets []room, results []roomResult, empty, failed []string) error {
	n := 0
	p := people{}
	for _, res := range results {
		n += len(res.messages)
		p.add(res.messages...)
	}
	if n == 0 {
		fmt.Fprintf(w, "no messages for: %s (in %d %s)\n", query, len(targets), plural(len(targets), "room", "rooms"))
		if words {
			fmt.Fprintln(w, "--words matches whole words only; without it the text matches as a substring.")
		} else if len(strings.Fields(query)) > 1 {
			fmt.Fprintln(w, "The words must appear together, in this order; search for fewer words, or pass --words to match any of them.")
		}
		if len(failed) > 0 {
			fmt.Fprintf(w, "Could not search: %s\n", strings.Join(failed, ", "))
		}
		return nil
	}
	fmt.Fprintf(w, "# Search %q: %d %s in %d %s\n", query, n, plural(n, "message", "messages"), len(results), plural(len(results), "room", "rooms"))
	p.write(w)
	for _, res := range results {
		fmt.Fprintf(w, "\n## %s (%d)\n\n", res.room.title(), len(res.messages))
		for i := range res.messages {
			writeMessage(w, "-", &res.messages[i], lineOpts{maxChars: 400})
		}
		if res.more {
			fmt.Fprintf(w, "(first %d; raise --limit or narrow the search)\n", len(res.messages))
		}
	}
	if len(empty) > 0 {
		fmt.Fprintf(w, "\nNo matches in: %s\n", strings.Join(empty, ", "))
	}
	if len(failed) > 0 {
		fmt.Fprintf(w, "Could not search: %s\n", strings.Join(failed, ", "))
	}
	fmt.Fprintln(w, "\nrct context <id> shows the conversation around a message; rct thread <id> a whole thread.")
	return nil
}
