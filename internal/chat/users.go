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

// RunUsers implements `rct users`.
func RunUsers(args []string, stdout, stderr io.Writer) error {
	fs := cli.NewFlags("users", "[TEXT]", "Users from the directory: username, full name, nickname, and email where the server\n"+
		"shows it. TEXT matches the username or name (the server's search fields).", stderr)
	limit := fs.Int("limit", 50, "maximum results")
	asJSON := fs.Bool("json", false, "print the raw API responses")
	pos, err := cli.ParseFlags(fs, args)
	if err != nil {
		return err
	}
	text := strings.Join(pos, " ")
	if err = checkRange("limit", *limit, 1, 5000); err != nil {
		return err
	}
	s, err := newSession(stderr)
	if err != nil {
		return err
	}
	var users []rocketchat.User
	var raws []json.RawMessage
	total := 0
	for len(users) < *limit {
		params := url.Values{"type": {"users"}, "count": {fmt.Sprint(min(100, *limit-len(users)))}, "offset": {fmt.Sprint(len(users))}}
		if text != "" {
			params.Set("text", text)
		}
		var res struct {
			Result []rocketchat.User `json:"result"`
			Total  int               `json:"total"`
		}
		raw, err := s.c.Get("/api/v1/directory", params, &res)
		if err != nil {
			return err
		}
		raws = append(raws, raw)
		users = append(users, res.Result...)
		total = res.Total
		if len(res.Result) == 0 || len(users) >= total {
			break
		}
	}
	if *asJSON {
		return cli.MarshalJSON(stdout, raws)
	}
	if len(users) == 0 {
		fmt.Fprintf(stdout, "no users match %q\n", text)
		return nil
	}
	fmt.Fprint(stdout, "# Users")
	if text != "" {
		fmt.Fprintf(stdout, " matching %q", text)
	}
	fmt.Fprintf(stdout, " (%d of %d)\n\n", len(users), total)
	for _, u := range users {
		parts := []string{cli.Or(u.Username, "?")}
		if u.Name != "" {
			parts[0] += " — " + u.Name
		}
		if u.Nickname != "" {
			parts = append(parts, "nickname "+u.Nickname)
		}
		for _, e := range u.Emails {
			parts = append(parts, e.Address)
		}
		parts = append(parts, "id "+u.ID)
		fmt.Fprintf(stdout, "- %s\n", strings.Join(parts, " · "))
	}
	if total > len(users) {
		fmt.Fprintf(stdout, "\nshowing %d of %d; raise --limit or pass part of a name\n", len(users), total)
	}
	return nil
}
