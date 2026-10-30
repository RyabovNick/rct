// Package chat implements the message commands: rooms, users, search,
// context, thread, threads and history.
package chat

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/RyabovNick/rct/internal/cli"
	"github.com/RyabovNick/rct/internal/rocketchat"
)

// room is what the commands need to know about a room, from the user's
// subscription and rooms.get, or from rooms.info for one they are not in.
type room struct {
	ID, T, Name, FName, PRID, Topic string
	LastActivity                    time.Time
	Users, Unread                   int
	Archived, Hidden, Member        bool
}

func (r room) kind() string {
	switch {
	case r.T == "l":
		return "livechat"
	case r.PRID != "":
		return "discussion"
	case r.T == "d":
		return "dm"
	case r.T == "p":
		return "group"
	}
	return "channel"
}

// label is how agents name a room back to rct: #channel, @username, or the
// display name of a discussion, whose name is an id.
func (r room) label() string {
	switch r.kind() {
	case "dm":
		if strings.Contains(r.Name, ",") {
			return "@" + cli.Or(r.FName, r.Name)
		}
		return "@" + r.Name
	case "discussion":
		return "#" + cli.Or(r.FName, r.Name)
	}
	return "#" + cli.Or(r.Name, r.ID)
}

// title is the label with the display name when it adds something, such as
// a DM partner's full name.
func (r room) title() string {
	if r.kind() != "discussion" && r.FName != "" && !strings.EqualFold(r.FName, r.Name) {
		return r.label() + " — " + r.FName
	}
	return r.label()
}

// path is the room's web path, which message links extend with ?msg=.
func (r room) path() string {
	switch r.T {
	case "d":
		return "/direct/" + r.ID
	case "p":
		return "/group/" + url.PathEscape(cli.Or(r.Name, r.ID))
	case "l":
		return "/live/" + r.ID
	}
	return "/channel/" + url.PathEscape(cli.Or(r.Name, r.ID))
}

func fromRoom(r *rocketchat.Room, member bool) room {
	return room{
		ID: r.ID, T: r.T, Name: r.Name, FName: r.FName, PRID: r.PRID, Topic: r.Topic,
		LastActivity: r.LastActivity(), Users: r.UsersCount, Archived: r.Archived, Member: member,
	}
}

// session is one command's client plus what it has already fetched.
type session struct {
	c        *rocketchat.Client
	stderr   io.Writer
	subs     []rocketchat.Subscription
	rawSubs  json.RawMessage
	rooms    []room
	rawRooms json.RawMessage
}

func newSession(stderr io.Writer) (*session, error) {
	c, err := rocketchat.NewClient(stderr)
	if err != nil {
		return nil, err
	}
	return &session{c: c, stderr: stderr}, nil
}

// subscriptions lists every room the user is in, DMs included, in one request.
func (s *session) subscriptions() ([]rocketchat.Subscription, error) {
	if s.rawSubs != nil {
		return s.subs, nil
	}
	var res struct {
		Update []rocketchat.Subscription `json:"update"`
	}
	raw, err := s.c.Get("/api/v1/subscriptions.get", nil, &res)
	if err != nil {
		return nil, err
	}
	s.subs, s.rawSubs = res.Update, raw
	return s.subs, nil
}

func fromSub(sub *rocketchat.Subscription) room {
	return room{
		ID: sub.RID, T: sub.T, Name: sub.Name, FName: sub.FName, PRID: sub.PRID,
		Unread: sub.Unread, Archived: sub.Archived, Hidden: !sub.Open, Member: true,
	}
}

// memberRooms joins the subscriptions with rooms.get, which adds the last
// activity, topic and member count, and sorts them most recently active first.
func (s *session) memberRooms() ([]room, error) {
	if s.rooms != nil {
		return s.rooms, nil
	}
	subs, err := s.subscriptions()
	if err != nil {
		return nil, err
	}
	var res struct {
		Update []rocketchat.Room `json:"update"`
	}
	raw, err := s.c.Get("/api/v1/rooms.get", nil, &res)
	if err != nil {
		return nil, err
	}
	byID := map[string]*rocketchat.Room{}
	for i := range res.Update {
		byID[res.Update[i].ID] = &res.Update[i]
	}
	rooms := make([]room, 0, len(subs))
	for i := range subs {
		r := fromSub(&subs[i])
		if info := byID[r.ID]; info != nil {
			r.Topic, r.Users, r.LastActivity = info.Topic, info.UsersCount, info.LastActivity()
			r.Archived = r.Archived || info.Archived
		}
		rooms = append(rooms, r)
	}
	slices.SortStableFunc(rooms, func(a, b room) int { return b.LastActivity.Compare(a.LastActivity) })
	s.rooms, s.rawRooms = rooms, raw
	return rooms, nil
}

// roomByID finds a room the user is in, or asks rooms.info for any other.
func (s *session) roomByID(id string) (room, error) {
	subs, err := s.subscriptions()
	if err != nil {
		return room{}, err
	}
	for i := range subs {
		if subs[i].RID == id {
			return fromSub(&subs[i]), nil
		}
	}
	return s.roomInfo(url.Values{"roomId": {id}})
}

func (s *session) roomInfo(params url.Values) (room, error) {
	var res struct {
		Room *rocketchat.Room `json:"room"`
	}
	if _, err := s.c.Get("/api/v1/rooms.info", params, &res); err != nil {
		return room{}, err
	}
	if res.Room == nil {
		return room{}, cli.Fail(cli.ExitNotFound, "no room for %s", params.Encode())
	}
	return fromRoom(res.Room, false), nil
}

// link parses ref as a URL on the configured server. Links carry no token,
// but one from another server would name rooms and messages rct cannot see.
func (s *session) link(ref string) (*url.URL, bool, error) {
	if !strings.HasPrefix(ref, "http://") && !strings.HasPrefix(ref, "https://") {
		return nil, false, nil
	}
	u, err := url.Parse(ref)
	if err != nil {
		return nil, false, cli.Fail(cli.ExitUsage, "bad link %q: %v", ref, err)
	}
	base, _ := url.Parse(s.c.Base)
	if !strings.EqualFold(u.Host, base.Host) {
		return nil, false, cli.Fail(cli.ExitUsage, "%s is a link to %s, but rct is configured for %s", ref, u.Host, base.Host)
	}
	return u, true, nil
}

var idRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// messageID takes a message id or a link to one: ?msg=ID, or /thread/ID.
func (s *session) messageID(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	u, isLink, err := s.link(ref)
	if err != nil {
		return "", err
	}
	if isLink {
		if id := u.Query().Get("msg"); id != "" {
			return id, nil
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if i := slices.Index(parts, "thread"); i >= 0 && i+1 < len(parts) {
			return parts[i+1], nil
		}
		return "", cli.Fail(cli.ExitUsage, "%s links to no message (no ?msg= or /thread/ID)", ref)
	}
	if !idRE.MatchString(ref) {
		return "", cli.Fail(cli.ExitUsage, "%q is not a message id or link", ref)
	}
	return ref, nil
}

// message fetches one message. Rocket.Chat answers a missing or hidden one
// with a bare HTTP 400.
func (s *session) message(id string) (rocketchat.Message, error) {
	var res struct {
		Message json.RawMessage `json:"message"`
	}
	if _, err := s.c.Get("/api/v1/chat.getMessage", url.Values{"msgId": {id}}, &res); err != nil {
		if cli.Code(err) == cli.ExitUsage {
			return rocketchat.Message{}, cli.Fail(cli.ExitNotFound, "message %s not found, or not visible to you", id)
		}
		return rocketchat.Message{}, err
	}
	msgs, err := rocketchat.DecodeMessages([]json.RawMessage{res.Message})
	if err != nil {
		return rocketchat.Message{}, err
	}
	return msgs[0], nil
}

// resolveRoom finds a room by id, name, display name or link. @name prefers
// direct messages and #name the others. Each tier, from exact id down to
// substring, must match exactly one room; else rct lists the candidates.
// A public channel the user is not in is looked up by name last.
func (s *session) resolveRoom(ref string) (room, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return room{}, cli.Fail(cli.ExitUsage, "empty room name")
	}
	u, isLink, err := s.link(ref)
	if err != nil {
		return room{}, err
	}
	want := ""
	if isLink {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) < 2 {
			return room{}, cli.Fail(cli.ExitUsage, "%s does not link to a room", ref)
		}
		if parts[0] == "direct" {
			want = "dm"
		}
		ref, _ = url.PathUnescape(parts[1])
	}
	switch ref[0] {
	case '@':
		want, ref = "dm", ref[1:]
	case '#':
		want, ref = "room", ref[1:]
	}
	subs, err := s.subscriptions()
	if err != nil {
		return room{}, err
	}
	low := strings.ToLower(ref)
	tiers := []func(r room) bool{
		func(r room) bool { return r.ID == ref },
		func(r room) bool { return strings.EqualFold(r.Name, ref) || strings.EqualFold(r.FName, ref) },
		func(r room) bool {
			return strings.HasPrefix(strings.ToLower(r.Name), low) || strings.HasPrefix(strings.ToLower(r.FName), low)
		},
		func(r room) bool {
			return strings.Contains(strings.ToLower(r.Name), low) || strings.Contains(strings.ToLower(r.FName), low)
		},
	}
	for _, match := range tiers {
		var found []room
		for i := range subs {
			if r := fromSub(&subs[i]); match(r) {
				found = append(found, r)
			}
		}
		if want != "" {
			if narrowed := slices.DeleteFunc(slices.Clone(found), func(r room) bool { return (r.kind() == "dm") != (want == "dm") }); len(narrowed) > 0 {
				found = narrowed
			}
		}
		switch len(found) {
		case 0:
			continue
		case 1:
			return found[0], nil
		}
		return room{}, ambiguous(ref, found)
	}
	r, err := s.roomInfo(url.Values{"roomName": {ref}})
	if cli.Code(err) == cli.ExitNotFound || cli.Code(err) == cli.ExitUsage {
		return room{}, cli.Fail(cli.ExitNotFound, "no room matches %q among your %d rooms; run rct rooms to list them, or rct rooms --public %q for channels you are not in",
			ref, len(subs), ref)
	}
	return r, err
}

func ambiguous(ref string, found []room) error {
	slices.SortFunc(found, func(a, b room) int { return cmp.Compare(a.label(), b.label()) })
	var b strings.Builder
	fmt.Fprintf(&b, "%q matches %d rooms; pass one exactly (a #name, @username or id):", ref, len(found))
	for i, r := range found {
		if i == 10 {
			fmt.Fprintf(&b, "\n  … and %d more", len(found)-10)
			break
		}
		fmt.Fprintf(&b, "\n  %s (%s, id %s)", r.title(), r.kind(), r.ID)
	}
	return cli.Fail(cli.ExitUsage, "%s", b.String())
}

// apiTime formats a time as the REST API's latest/oldest parameters expect.
func apiTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// history returns room messages between oldest and latest (both optional),
// newest first, without thread replies: those belong to their threads.
func (s *session) history(r room, oldest, latest time.Time, count, offset int) ([]rocketchat.Message, int, error) {
	endpoint := map[string]string{"c": "channels.history", "p": "groups.history", "d": "im.history"}[r.T]
	if endpoint == "" {
		return nil, 0, cli.Fail(cli.ExitUsage, "%s is a %s room; rct reads channels, groups, discussions and direct messages", r.label(), r.kind())
	}
	params := url.Values{"roomId": {r.ID}, "count": {fmt.Sprint(count)}, "showThreadMessages": {"false"}}
	if offset > 0 {
		params.Set("offset", fmt.Sprint(offset))
	}
	if !oldest.IsZero() {
		params.Set("oldest", apiTime(oldest))
	}
	if !latest.IsZero() {
		params.Set("latest", apiTime(latest))
	}
	var res struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if _, err := s.c.Get("/api/v1/"+endpoint, params, &res); err != nil {
		return nil, 0, err
	}
	msgs, err := rocketchat.DecodeMessages(res.Messages)
	return visible(msgs, false), len(res.Messages), err
}

// visible drops system messages (joins, renames, …) and, outside a thread
// view, replies that were not also sent to the room.
func visible(msgs []rocketchat.Message, inThread bool) []rocketchat.Message {
	return slices.DeleteFunc(msgs, func(m rocketchat.Message) bool {
		return m.T != "" || (!inThread && m.TMID != "" && !m.TShow)
	})
}

func checkRange(name string, v, lo, hi int) error {
	if v < lo || v > hi {
		return cli.Fail(cli.ExitUsage, "--%s must be between %d and %d", name, lo, hi)
	}
	return nil
}
