// Package testenv is the shared test fixture: a fake Rocket.Chat server that
// follows the 8.5 REST API, config and environment helpers, and a way to run
// a command.
package testenv

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RyabovNick/rct/internal/cli"
)

// Command is the signature of every rct command.
type Command func(args []string, stdout, stderr io.Writer) error

// Run runs the command named by args[0] from cmds and returns its output and exit code.
func Run(t *testing.T, cmds map[string]Command, args ...string) (string, string, int) {
	t.Helper()
	cmd, ok := cmds[args[0]]
	if !ok {
		t.Fatalf("no command %q in this package", args[0])
	}
	var out, errb bytes.Buffer
	code := cli.Report(cmd(args[1:], &out, &errb), &errb)
	return out.String(), errb.String(), code
}

// The credentials FakeServer accepts.
const (
	Token  = "test-token"
	UserID = "U1"
)

// Base is when the fixture's conversations start: 2026-10-28 10:00 UTC.
var Base = time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)

type user struct {
	ID       string `json:"_id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

var (
	me     = user{"U1", "ivanovii", "Ivanov Ivan Ivanovich"}
	walker = user{"U2", "walkerjj", "Walker John Johnson"}
	petrov = user{"U3", "petrovpp", "Petrov Petr Petrovich"}
)

type message struct {
	ID          string              `json:"_id"`
	RID         string              `json:"rid"`
	Msg         string              `json:"msg"`
	TS          time.Time           `json:"ts"`
	U           user                `json:"u"`
	T           string              `json:"t,omitempty"`
	TMID        string              `json:"tmid,omitempty"`
	TShow       bool                `json:"tshow,omitempty"`
	TCount      int                 `json:"tcount,omitempty"`
	TLM         *time.Time          `json:"tlm,omitempty"`
	EditedAt    *time.Time          `json:"editedAt,omitempty"`
	File        map[string]string   `json:"file,omitempty"`
	Attachments []map[string]string `json:"attachments,omitempty"`
}

type room struct {
	ID         string     `json:"_id"`
	T          string     `json:"t"`
	Name       string     `json:"name,omitempty"`
	FName      string     `json:"fname,omitempty"`
	Topic      string     `json:"topic,omitempty"`
	UsersCount int        `json:"usersCount"`
	PRID       string     `json:"prid,omitempty"`
	LM         *time.Time `json:"lm,omitempty"`
	// subscribed rooms appear in subscriptions.get; sub overrides name and fname there, as for DMs.
	member         bool
	subName, subFN string
}

// Fake is the running fake server and the requests it has seen.
type Fake struct {
	*httptest.Server
	mu        sync.Mutex
	requests  []string
	rateLimit map[string]bool
}

// Requests returns the paths requested so far, each with its query.
func (f *Fake) Requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

// Count returns how many requests went to path.
func (f *Fake) Count(path string) int {
	n := 0
	for _, r := range f.Requests() {
		if p, _, _ := strings.Cut(r, "?"); p == path {
			n++
		}
	}
	return n
}

// RateLimitOnce makes the next request to path answer 429, as Rocket.Chat
// does when an endpoint's rate limit is used up.
func (f *Fake) RateLimitOnce(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rateLimit[path] = true
}

func minutes(n int) time.Time { return Base.Add(time.Duration(n) * time.Minute) }

// fixture builds the rooms and messages: #dev-backend with a thread, a quote
// and a file; a private group with a discussion; two DMs; the busy
// #dev-frontend with 250 messages a minute apart; and #announcements, a public
// channel the user has not joined.
func fixture() ([]room, []message) {
	var msgs []message
	add := func(id, rid string, u user, ts time.Time, text string) *message {
		msgs = append(msgs, message{ID: id, RID: rid, U: u, TS: ts, Msg: text})
		return &msgs[len(msgs)-1]
	}
	people := []user{me, walker, petrov}
	for i := 1; i <= 30; i++ {
		add(fmt.Sprintf("b%02d", i), "R1", people[i%3], minutes(i), fmt.Sprintf("backend message %d", i))
	}
	msgs[4].Msg, msgs[4].U = "Deploy of PROJ-123 failed on staging", walker
	msgs[9].Msg, msgs[9].TCount, msgs[9].TLM = "Release plan for 2.3", 3, new(minutes(14))
	msgs[11].Msg, msgs[11].T = "", "uj"
	msgs[14].Msg = "see the log"
	msgs[14].File = map[string]string{"_id": "F1", "name": "server.log", "type": "text/plain"}
	msgs[14].Attachments = []map[string]string{
		{"title": "server.log", "type": "file"},
		{"message_link": "BASE/channel/dev-backend?msg=b05", "author_name": "walkerjj", "text": "Deploy of PROJ-123 failed on staging"},
	}
	msgs[19].Msg, msgs[19].EditedAt = "line one\nline two", new(minutes(25))
	add("r1", "R1", walker, minutes(10).Add(30*time.Second), "PROJ-123 is blocked by the DB migration").TMID = "b10"
	add("r2", "R1", petrov, minutes(12).Add(30*time.Second), "let's postpone").TMID = "b10"
	r3 := add("r3", "R1", me, minutes(14), "Agreed, moving the release to Friday")
	r3.TMID, r3.TShow = "b10", true

	add("g1", "R2", petrov, minutes(40), "release checklist is in Confluence")
	add("g2", "R2", me, minutes(41), "thanks")
	add("d1", "U1U2", walker, minutes(50), "PROJ-123: can you check the migration?")
	add("d2", "U1U2", me, minutes(51), "looking")
	add("k1", "R4", walker, minutes(60), "the rollback script for 2.3 lives in ops/rollback.sh")
	for i := 1; i <= 250; i++ {
		add(fmt.Sprintf("f%03d", i), "R5", people[i%3], Base.Add(-8*24*time.Hour+time.Duration(i)*time.Minute), fmt.Sprintf("frontend message %d", i))
	}
	add("a1", "R6", petrov, minutes(5), "maintenance window on Saturday")

	rooms := []room{
		{ID: "R1", T: "c", Name: "dev-backend", FName: "dev-backend", Topic: "Backend team chat", UsersCount: 12, member: true},
		{ID: "R2", T: "p", Name: "release-team", FName: "Release Team", UsersCount: 4, member: true},
		{ID: "U1U2", T: "d", UsersCount: 2, member: true, subName: "walkerjj", subFN: "Walker John Johnson"},
		{ID: "R4", T: "p", Name: "Dk3sP", FName: "Release 2.3 issues", PRID: "R2", UsersCount: 3, member: true},
		{ID: "R5", T: "c", Name: "dev-frontend", FName: "dev-frontend", UsersCount: 30, member: true},
		{ID: "R6", T: "c", Name: "announcements", FName: "announcements", UsersCount: 500},
		{ID: "U1U3", T: "d", UsersCount: 2, member: true, subName: "petrovpp", subFN: "Petrov Petr Petrovich"},
	}
	for i := range rooms {
		for _, m := range msgs {
			if m.RID == rooms[i].ID && (rooms[i].LM == nil || m.TS.After(*rooms[i].LM)) {
				rooms[i].LM = new(m.TS)
			}
		}
	}
	return rooms, msgs
}

// FakeServer serves the Rocket.Chat endpoints rct uses.
func FakeServer(t *testing.T) *Fake {
	t.Helper()
	f := &Fake{rateLimit: map[string]bool{}}
	rooms, msgs := fixture()
	mux := http.NewServeMux()

	reply := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		data, err := json.Marshal(v)
		if err != nil {
			t.Errorf("encoding a reply: %v", err)
		}
		if _, err := w.Write(bytes.ReplaceAll(data, []byte("BASE"), []byte(f.URL))); err != nil {
			t.Errorf("writing a reply: %v", err)
		}
	}
	failure := func(w http.ResponseWriter, body map[string]any) {
		body["success"] = false
		reply(w, http.StatusBadRequest, body)
	}
	handle := func(path string, h func(w http.ResponseWriter, q map[string]string)) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("%s %s: rct must only send GET", r.Method, path)
			}
			f.mu.Lock()
			f.requests = append(f.requests, r.URL.Path+"?"+r.URL.RawQuery)
			limited := f.rateLimit[path]
			delete(f.rateLimit, path)
			f.mu.Unlock()
			if r.Header.Get("X-User-Id") != UserID || r.Header.Get("X-Auth-Token") != Token {
				reply(w, http.StatusUnauthorized, map[string]any{"status": "error", "message": "You must be logged in to do this."})
				return
			}
			if limited {
				w.Header().Set("X-RateLimit-Limit", "10")
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(2*time.Second).UnixMilli(), 10))
				reply(w, http.StatusTooManyRequests, map[string]any{"success": false, "error": "Error, too many requests. Please slow down."})
				return
			}
			q := map[string]string{}
			for k, v := range r.URL.Query() {
				q[k] = v[0]
			}
			h(w, q)
		})
	}
	page := func(q map[string]string, n int) (lo, hi int) {
		count, offset := 50, 0
		if c, err := strconv.Atoi(q["count"]); err == nil {
			count = c
		}
		if o, err := strconv.Atoi(q["offset"]); err == nil {
			offset = o
		}
		count = min(count, 100)
		return min(offset, n), min(offset+count, n)
	}
	findRoom := func(id string) *room {
		for i := range rooms {
			if rooms[i].ID == id {
				return &rooms[i]
			}
		}
		return nil
	}
	findMsg := func(id string) *message {
		for i := range msgs {
			if msgs[i].ID == id {
				return &msgs[i]
			}
		}
		return nil
	}
	newestFirst := func(a, b message) int { return b.TS.Compare(a.TS) }

	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth-Token") != Token {
			reply(w, 200, map[string]any{"success": true})
			return
		}
		reply(w, 200, map[string]any{"info": map[string]string{"version": "8.5.0"}, "success": true})
	})
	handle("/api/v1/me", func(w http.ResponseWriter, _ map[string]string) {
		reply(w, 200, map[string]any{"_id": me.ID, "username": me.Username, "name": me.Name,
			"emails": []map[string]any{{"address": "ivanovii@example.com", "verified": true}}, "success": true})
	})
	handle("/api/v1/subscriptions.get", func(w http.ResponseWriter, _ map[string]string) {
		var subs []map[string]any
		for _, r := range rooms {
			if r.member {
				subs = append(subs, map[string]any{"_id": "S" + r.ID, "rid": r.ID, "t": r.T, "name": cmpOr(r.subName, r.Name),
					"fname": cmpOr(r.subFN, r.FName), "prid": r.PRID, "open": r.ID != "U1U3", "unread": map[bool]int{true: 2}[r.ID == "U1U2"]})
			}
		}
		reply(w, 200, map[string]any{"update": subs, "remove": []any{}, "success": true})
	})
	handle("/api/v1/rooms.get", func(w http.ResponseWriter, _ map[string]string) {
		var out []room
		for _, r := range rooms {
			if r.member {
				out = append(out, r)
			}
		}
		reply(w, 200, map[string]any{"update": out, "remove": []any{}, "success": true})
	})
	handle("/api/v1/rooms.info", func(w http.ResponseWriter, q map[string]string) {
		for _, r := range rooms {
			if (q["roomId"] != "" && r.ID == q["roomId"]) || (q["roomName"] != "" && r.Name == q["roomName"]) {
				reply(w, 200, map[string]any{"room": r, "success": true})
				return
			}
		}
		failure(w, map[string]any{"error": "not-allowed", "errorType": "Not Allowed"})
	})
	handle("/api/v1/directory", func(w http.ResponseWriter, q map[string]string) {
		text := strings.ToLower(q["text"])
		var result []any
		switch q["type"] {
		case "users":
			for _, u := range []user{me, walker, petrov} {
				if strings.Contains(u.Username, text) || strings.Contains(strings.ToLower(u.Name), text) {
					result = append(result, u)
				}
			}
		case "channels":
			for _, r := range rooms {
				if r.T == "c" && strings.Contains(r.Name, text) {
					result = append(result, map[string]any{"_id": r.ID, "t": r.T, "name": r.Name, "fname": r.FName,
						"usersCount": r.UsersCount, "topic": r.Topic, "lastMessage": map[string]any{"ts": r.LM}})
				}
			}
		default:
			failure(w, map[string]any{"error": "invalid type"})
			return
		}
		lo, hi := page(q, len(result))
		reply(w, 200, map[string]any{"result": result[lo:hi], "count": hi - lo, "offset": lo, "total": len(result), "success": true})
	})
	handle("/api/v1/chat.getMessage", func(w http.ResponseWriter, q map[string]string) {
		if m := findMsg(q["msgId"]); m != nil {
			reply(w, 200, map[string]any{"message": m, "success": true})
			return
		}
		failure(w, map[string]any{})
	})
	handle("/api/v1/chat.search", func(w http.ResponseWriter, q map[string]string) {
		if findRoom(q["roomId"]) == nil {
			failure(w, map[string]any{"error": "error-not-allowed"})
			return
		}
		match, err := searchMatcher(q["searchText"])
		if err != nil {
			failure(w, map[string]any{"error": err.Error()})
			return
		}
		var found []message
		for _, m := range msgs {
			if m.RID == q["roomId"] && m.T == "" && match(m) {
				found = append(found, m)
			}
		}
		slices.SortFunc(found, newestFirst)
		lo, hi := page(q, len(found))
		reply(w, 200, map[string]any{"messages": found[lo:hi], "success": true})
	})
	history := func(t, endpoint string, threadsByDefault bool) {
		handle("/api/v1/"+endpoint, func(w http.ResponseWriter, q map[string]string) {
			r := findRoom(q["roomId"])
			if r == nil || r.T != t {
				failure(w, map[string]any{"error": "The required \"roomId\" param provided does not match any room [error-room-not-found]", "errorType": "error-room-not-found"})
				return
			}
			latest, oldest := time.Now(), time.Time{}
			if v := q["latest"]; v != "" {
				latest, _ = time.Parse(time.RFC3339, v)
			}
			if v := q["oldest"]; v != "" {
				oldest, _ = time.Parse(time.RFC3339, v)
			}
			threads := threadsByDefault
			if v, ok := q["showThreadMessages"]; ok {
				threads = v == "true"
			}
			var found []message
			for _, m := range msgs {
				if m.RID == r.ID && m.TS.Before(latest) && (oldest.IsZero() || m.TS.After(oldest)) && (threads || m.TMID == "" || m.TShow) {
					found = append(found, m)
				}
			}
			slices.SortFunc(found, newestFirst)
			lo, hi := page(q, len(found))
			reply(w, 200, map[string]any{"messages": found[lo:hi], "success": true})
		})
	}
	// As in 8.5: channels and DMs hide thread replies unless asked, groups show them.
	history("c", "channels.history", false)
	history("p", "groups.history", true)
	history("d", "im.history", false)
	handle("/api/v1/chat.getThreadMessages", func(w http.ResponseWriter, q map[string]string) {
		if findMsg(q["tmid"]) == nil {
			failure(w, map[string]any{"error": "Invalid Message [error-invalid-message]", "errorType": "error-invalid-message"})
			return
		}
		var found []message
		for _, m := range msgs {
			if m.TMID == q["tmid"] {
				found = append(found, m)
			}
		}
		slices.SortFunc(found, func(a, b message) int { return a.TS.Compare(b.TS) })
		lo, hi := page(q, len(found))
		reply(w, 200, map[string]any{"messages": found[lo:hi], "count": hi - lo, "offset": lo, "total": len(found), "success": true})
	})
	handle("/api/v1/chat.getThreadsList", func(w http.ResponseWriter, q map[string]string) {
		var found []message
		for _, m := range msgs {
			if m.RID == q["rid"] && m.TCount > 0 && strings.Contains(strings.ToLower(m.Msg), strings.ToLower(q["text"])) {
				found = append(found, m)
			}
		}
		lo, hi := page(q, len(found))
		reply(w, 200, map[string]any{"threads": found[lo:hi], "count": hi - lo, "offset": lo, "total": len(found), "success": true})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, http.StatusNotFound, map[string]any{"success": false, "error": "Resource not found"})
	})

	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

var (
	fromRE  = regexp.MustCompile(`from:(\S+)`)
	afterRE = regexp.MustCompile(`after:(\d{2})-(\d{2})-(\d{4})`)
	regexRE = regexp.MustCompile(`^/(.+)/([a-z]*)$`)
)

// searchMatcher follows Rocket.Chat's message search: from: and after:
// operators, then a /regex/flags or a whole-word search where any word matches.
func searchMatcher(text string) (func(message) bool, error) {
	var tests []func(message) bool
	if m := fromRE.FindStringSubmatch(text); m != nil {
		tests = append(tests, func(msg message) bool { return msg.U.Username == m[1] })
		text = fromRE.ReplaceAllString(text, "")
	}
	if m := afterRE.FindStringSubmatch(text); m != nil {
		day, _ := time.Parse("02-01-2006", m[1]+"-"+m[2]+"-"+m[3])
		tests = append(tests, func(msg message) bool { return !msg.TS.Before(day.AddDate(0, 0, 1)) })
		text = afterRE.ReplaceAllString(text, "")
	}
	text = strings.TrimSpace(text)
	if m := regexRE.FindStringSubmatch(text); m != nil {
		expr := m[1]
		if strings.Contains(m[2], "i") {
			expr = "(?i)" + expr
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("bad regex: %w", err)
		}
		tests = append(tests, func(msg message) bool { return re.MatchString(msg.Msg) })
	} else if text != "" {
		words := strings.Fields(strings.ToLower(text))
		tests = append(tests, func(msg message) bool {
			for _, w := range strings.FieldsFunc(strings.ToLower(msg.Msg), func(r rune) bool {
				return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r < 0x80
			}) {
				if slices.Contains(words, w) {
					return true
				}
			}
			return false
		})
	}
	return func(m message) bool {
		for _, test := range tests {
			if !test(m) {
				return false
			}
		}
		return true
	}, nil
}

// ClearEnv keeps a developer's real credentials out of the tests.
func ClearEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{"ROCKETCHAT_URL", "ROCKETCHAT_USER_ID", "ROCKETCHAT_TOKEN"} {
		t.Setenv(v, "")
	}
}

// WriteConfig writes a config file with mode and points RCT_CONFIG at it.
func WriteConfig(t *testing.T, body string, mode os.FileMode) {
	t.Helper()
	ClearEnv(t)
	path := filepath.Join(t.TempDir(), "config.ini")
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RCT_CONFIG", path)
}

// Setup starts FakeServer and configures rct against it.
func Setup(t *testing.T) *Fake {
	t.Helper()
	f := FakeServer(t)
	WriteConfig(t, fmt.Sprintf("# test\n[rocketchat]\nurl = %s/\nuser_id = %s\ntoken = %s\n", f.URL, UserID, Token), 0o600)
	return f
}

// MustContain fails the test for each of wants that s lacks.
func MustContain(t *testing.T, s string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(s, w) {
			t.Errorf("output lacks %q:\n%s", w, s)
		}
	}
}

// MustNotContain fails the test for each of unwanted that s holds.
func MustNotContain(t *testing.T, s string, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if strings.Contains(s, w) {
			t.Errorf("output holds %q:\n%s", w, s)
		}
	}
}
