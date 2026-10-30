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

var roomGroups = []struct{ kind, title, one string }{
	{"channel", "Channels", "channel"},
	{"group", "Private groups", "private group"},
	{"discussion", "Discussions", "discussion"},
	{"dm", "Direct messages", "direct message"},
	{"livechat", "Livechat", "livechat"},
}

func parseKinds(list string) (map[string]bool, error) {
	if list == "" {
		return nil, nil
	}
	kinds := map[string]bool{}
	for k := range strings.SplitSeq(list, ",") {
		k = strings.TrimSpace(k)
		known := false
		for _, g := range roomGroups {
			known = known || g.kind == k
		}
		if !known {
			return nil, cli.Fail(cli.ExitUsage, "unknown room type %q; use channel, group, discussion, dm or livechat", k)
		}
		kinds[k] = true
	}
	return kinds, nil
}

func roomMatches(r room, text string) bool {
	if text == "" {
		return true
	}
	t := strings.ToLower(text)
	return strings.Contains(strings.ToLower(r.Name), t) || strings.Contains(strings.ToLower(r.FName), t) || r.ID == text
}

func roomLine(r room, parents map[string]room) string {
	parts := []string{r.title()}
	if r.kind() == "discussion" {
		if p, ok := parents[r.PRID]; ok {
			parts = append(parts, "in "+p.label())
		}
	}
	if r.Users > 0 && r.kind() != "dm" {
		parts = append(parts, fmt.Sprintf("%d members", r.Users))
	}
	if !r.LastActivity.IsZero() {
		parts = append(parts, "last "+stamp(r.LastActivity))
	}
	if r.Unread > 0 {
		parts = append(parts, fmt.Sprintf("%d unread", r.Unread))
	}
	if r.Archived {
		parts = append(parts, "archived")
	}
	if r.Hidden {
		parts = append(parts, "hidden")
	}
	parts = append(parts, "id "+r.ID)
	line := "- " + strings.Join(parts, " · ")
	if t := strings.TrimSpace(r.Topic); t != "" {
		line += "\n  topic: " + truncateRunes(strings.ReplaceAll(t, "\n", " "), 150)
	}
	return line
}

// RunRooms implements `rct rooms`.
func RunRooms(args []string, stdout, stderr io.Writer) error {
	fs := cli.NewFlags("rooms", "[TEXT]", "The rooms you are in: channels, private groups, discussions and direct messages,\n"+
		"with name, display name (a DM partner's full name), last activity and id. TEXT filters\n"+
		"by name or display name. --public searches public channels, joined or not, instead.", stderr)
	kinds := fs.String("type", "", "only these types, comma-separated: channel, group, discussion, dm, livechat")
	public := fs.Bool("public", false, "search the directory of public channels instead of your rooms")
	limit := fs.Int("limit", 50, "maximum results with --public")
	asJSON := fs.Bool("json", false, "print the raw API responses")
	pos, err := cli.ParseFlags(fs, args)
	if err != nil {
		return err
	}
	text := strings.Join(pos, " ")
	want, err := parseKinds(*kinds)
	if err != nil {
		return err
	}
	if err = checkRange("limit", *limit, 1, 1000); err != nil {
		return err
	}
	s, err := newSession(stderr)
	if err != nil {
		return err
	}
	if *public {
		return publicRooms(s, text, *limit, *asJSON, stdout)
	}
	rooms, err := s.memberRooms()
	if err != nil {
		return err
	}
	if *asJSON {
		return cli.MarshalJSON(stdout, map[string]json.RawMessage{"subscriptions": s.rawSubs, "rooms": s.rawRooms})
	}
	parents := map[string]room{}
	for _, r := range rooms {
		parents[r.ID] = r
	}
	groups := map[string][]room{}
	n := 0
	for _, r := range rooms {
		if (want == nil || want[r.kind()]) && roomMatches(r, text) {
			groups[r.kind()] = append(groups[r.kind()], r)
			n++
		}
	}
	if n == 0 {
		if text != "" {
			fmt.Fprintf(stdout, "no rooms of yours match %q; rct rooms --public %q searches public channels you are not in\n", text, text)
		} else {
			fmt.Fprintln(stdout, "no rooms")
		}
		return nil
	}
	var counts []string
	for _, g := range roomGroups {
		if c := len(groups[g.kind]); c > 0 {
			counts = append(counts, fmt.Sprintf("%d %s", c, plural(c, g.one, strings.ToLower(g.title))))
		}
	}
	fmt.Fprintf(stdout, "# Rooms (%d): %s\n", n, strings.Join(counts, ", "))
	fmt.Fprintln(stdout, "Most recently active first. Pass a #name, @username or id as ROOM to other commands.")
	for _, g := range roomGroups {
		if len(groups[g.kind]) == 0 {
			continue
		}
		fmt.Fprintf(stdout, "\n## %s (%d)\n\n", g.title, len(groups[g.kind]))
		for _, r := range groups[g.kind] {
			fmt.Fprintln(stdout, roomLine(r, parents))
		}
	}
	return nil
}

func publicRooms(s *session, text string, limit int, asJSON bool, stdout io.Writer) error {
	var all []rocketchat.Room
	var raws []json.RawMessage
	total := 0
	for len(all) < limit {
		params := url.Values{"type": {"channels"}, "count": {fmt.Sprint(min(100, limit-len(all)))}, "offset": {fmt.Sprint(len(all))}}
		if text != "" {
			params.Set("text", text)
		}
		var res struct {
			Result []rocketchat.Room `json:"result"`
			Total  int               `json:"total"`
		}
		raw, err := s.c.Get("/api/v1/directory", params, &res)
		if err != nil {
			return err
		}
		raws = append(raws, raw)
		all = append(all, res.Result...)
		total = res.Total
		if len(res.Result) == 0 || len(all) >= total {
			break
		}
	}
	if asJSON {
		return cli.MarshalJSON(stdout, raws)
	}
	if len(all) == 0 {
		fmt.Fprintf(stdout, "no public channels match %q\n", text)
		return nil
	}
	subs, err := s.subscriptions()
	if err != nil {
		return err
	}
	member := map[string]bool{}
	for _, sub := range subs {
		member[sub.RID] = true
	}
	fmt.Fprintf(stdout, "# Public channels")
	if text != "" {
		fmt.Fprintf(stdout, " matching %q", text)
	}
	fmt.Fprintf(stdout, " (%d of %d)\n\n", len(all), total)
	for i := range all {
		r := fromRoom(&all[i], member[all[i].ID])
		line := roomLine(r, nil)
		if !r.Member {
			line = strings.Replace(line, " · id ", " · not joined · id ", 1)
		}
		fmt.Fprintln(stdout, line)
	}
	if total > len(all) {
		fmt.Fprintf(stdout, "\nshowing %d of %d; raise --limit or narrow TEXT\n", len(all), total)
	}
	return nil
}
