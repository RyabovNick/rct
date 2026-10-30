package chat

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/RyabovNick/rct/internal/cli"
	"github.com/RyabovNick/rct/internal/rocketchat"
	"github.com/RyabovNick/rct/internal/testenv"
)

func TestMain(m *testing.M) {
	// Message times print in local time; pin it so expectations hold anywhere.
	time.Local = time.UTC
	rocketchat.Sleep = func(time.Duration) {}
	os.Exit(m.Run())
}

func runRct(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	return testenv.Run(t, map[string]testenv.Command{
		"rooms": RunRooms, "users": RunUsers, "search": RunSearch, "context": RunContext,
		"thread": RunThread, "threads": RunThreads, "history": RunHistory,
	}, args...)
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	out, errs, code := runRct(t, args...)
	if code != 0 {
		t.Fatalf("rct %v: exit %d: %s\n%s", args, code, errs, out)
	}
	return out
}

func TestRooms(t *testing.T) {
	testenv.Setup(t)
	out := mustRun(t, "rooms")
	testenv.MustContain(t, out,
		"# Rooms (6): 2 channels, 1 private group, 1 discussion, 2 direct messages",
		"## Channels (2)\n\n- #dev-backend · 12 members · last 2026-10-28 10:30 · id R1\n  topic: Backend team chat\n- #dev-frontend",
		"- #release-team — Release Team · 4 members",
		"- #Release 2.3 issues · in #release-team · 3 members",
		"- @walkerjj — Walker John Johnson · last 2026-10-28 10:51 · 2 unread · id U1U2",
		"- @petrovpp — Petrov Petr Petrovich · hidden · id U1U3",
	)
	testenv.MustNotContain(t, out, "announcements")

	out = mustRun(t, "rooms", "walker")
	testenv.MustContain(t, out, "# Rooms (1): 1 direct message", "@walkerjj — Walker John Johnson")
	out = mustRun(t, "rooms", "--type", "dm,discussion")
	testenv.MustContain(t, out, "1 discussion, 2 direct messages")
	testenv.MustNotContain(t, out, "#dev-backend")
	out = mustRun(t, "rooms", "announce")
	testenv.MustContain(t, out, `no rooms of yours match "announce"; rct rooms --public "announce"`)
	if _, errs, code := runRct(t, "rooms", "--type", "team"); code != cli.ExitUsage || !strings.Contains(errs, `unknown room type "team"`) {
		t.Errorf("--type team: exit %d, %s", code, errs)
	}

	out = mustRun(t, "rooms", "--public")
	testenv.MustContain(t, out, "# Public channels (3 of 3)", "- #announcements · 500 members · last 2026-10-28 10:05 · not joined · id R6")
	testenv.MustNotContain(t, out, "#dev-backend · 12 members · last 2026-10-28 10:30 · not joined")
	out = mustRun(t, "rooms", "--public", "dev", "--limit", "1")
	testenv.MustContain(t, out, `# Public channels matching "dev" (1 of 2)`, "showing 1 of 2; raise --limit")

	out = mustRun(t, "rooms", "--json")
	type list struct {
		Update []any `json:"update"`
	}
	var v struct {
		Subscriptions list `json:"subscriptions"`
		Rooms         list `json:"rooms"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil || len(v.Subscriptions.Update) != 6 || len(v.Rooms.Update) != 6 {
		t.Errorf("rooms --json: %v\n%s", err, out)
	}
}

func TestUsers(t *testing.T) {
	testenv.Setup(t)
	out := mustRun(t, "users")
	testenv.MustContain(t, out, "# Users (3 of 3)", "- ivanovii — Ivanov Ivan Ivanovich · id U1", "- walkerjj — Walker John Johnson · id U2")
	out = mustRun(t, "users", "Walker", "John")
	testenv.MustContain(t, out, `# Users matching "Walker John" (1 of 1)`)
	out = mustRun(t, "users", "--limit", "2")
	testenv.MustContain(t, out, "(2 of 3)", "showing 2 of 3")
	testenv.MustContain(t, mustRun(t, "users", "nobody"), `no users match "nobody"`)
}

func TestResolveRoom(t *testing.T) {
	f := testenv.Setup(t)
	for ref, want := range map[string]string{
		"dev-backend":                   "# #dev-backend:",
		"#DEV-BACKEND":                  "# #dev-backend:",
		"R1":                            "# #dev-backend:",
		"Release Team":                  "# #release-team — Release Team:",
		"@walkerjj":                     "# @walkerjj — Walker John Johnson:",
		"walker john":                   "# @walkerjj — Walker John Johnson:",
		"2.3":                           "# #Release 2.3 issues:",
		f.URL + "/direct/U1U2":          "# @walkerjj — Walker John Johnson:",
		f.URL + "/channel/dev-backend/": "# #dev-backend:",
		"announcements":                 "# #announcements: 1 message",
	} {
		out, errs, code := runRct(t, "history", ref, "--limit", "1")
		if code != 0 || !strings.Contains(out, want) {
			t.Errorf("%q: exit %d, %s\n%s", ref, code, errs, out)
		}
	}
	_, errs, code := runRct(t, "history", "dev")
	if code != cli.ExitUsage {
		t.Errorf("ambiguous: exit %d", code)
	}
	testenv.MustContain(t, errs, `"dev" matches 2 rooms`, "#dev-backend (channel, id R1)", "#dev-frontend (channel, id R5)")
	_, errs, code = runRct(t, "history", "nowhere")
	if code != cli.ExitNotFound || !strings.Contains(errs, `no room matches "nowhere" among your 6 rooms`) {
		t.Errorf("unknown room: exit %d, %s", code, errs)
	}
	if _, errs, code := runRct(t, "history", "https://chat.elsewhere.example/channel/x"); code != cli.ExitUsage || !strings.Contains(errs, "chat.elsewhere.example") {
		t.Errorf("foreign link: exit %d, %s", code, errs)
	}
}

func TestSearchText(t *testing.T) {
	since := time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		text  string
		words bool
		since time.Time
		want  string
	}{
		{"PROJ-123", false, time.Time{}, `/PROJ-123/i`},
		{"from:walkerjj ops/rollback.sh (v2)", false, time.Time{}, `from:walkerjj /ops\x2Frollback\.sh \(v2\)/i`},
		{"/dead+lock/i", false, time.Time{}, `/dead+lock/i`},
		{"migration rollback is:pinned", true, since, `is:pinned after:25-10-2026 migration rollback`},
		{"has:url", false, time.Time{}, `has:url`},
	} {
		if got := searchText(tc.text, tc.words, tc.since); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.text, got, tc.want)
		}
	}
}

func TestSearch(t *testing.T) {
	f := testenv.Setup(t)
	out := mustRun(t, "search", "proj-123", "--room", "dev-backend")
	testenv.MustContain(t, out,
		`# Search "/proj-123/i": 2 messages in 1 room`,
		"People: walkerjj = Walker John Johnson",
		"## #dev-backend (2)",
		"- 2026-10-28 10:10 walkerjj [r1] · reply in thread b10: PROJ-123 is blocked by the DB migration\n- 2026-10-28 10:05 walkerjj [b05]: Deploy",
		"rct context <id>",
	)

	out, errs, code := runRct(t, "search", "PROJ-123", "--all", "--max-rooms", "3")
	if code != 0 {
		t.Fatalf("--all: exit %d: %s", code, errs)
	}
	testenv.MustContain(t, errs, "searching 3 of 6 rooms, most recently active first; raise --max-rooms (3 more)")
	testenv.MustContain(t, out, "1 message in 1 room", "## @walkerjj — Walker John Johnson (1)", "No matches in: #Release 2.3 issues, #release-team")

	out = mustRun(t, "search", "migration", "rollback", "--words", "--since", "2026-10-27", "--all")
	testenv.MustContain(t, out, "3 messages in 3 rooms", "[k1]", "[d1]", "[r1]")
	testenv.MustNotContain(t, out, "#dev-frontend (")
	out = mustRun(t, "search", "rollback", "script", "--room", "@walkerjj")
	testenv.MustContain(t, out, "no messages for: /rollback script/i (in 1 room)", "pass --words to match any of them")

	out = mustRun(t, "search", "from:petrovpp", "message", "--room", "R1", "--limit", "2")
	testenv.MustContain(t, out, "## #dev-backend (2)", "(first 2; raise --limit")
	testenv.MustNotContain(t, out, "walkerjj [")

	// A rate-limited request waits for the reset and is retried.
	f.RateLimitOnce("/api/v1/chat.search")
	out, errs, code = runRct(t, "search", "PROJ-123", "--room", "dev-backend")
	if code != 0 || !strings.Contains(errs, "rct: rate limited on chat.search; waiting") || !strings.Contains(out, "2 messages") {
		t.Errorf("rate limit: exit %d, %s\n%s", code, errs, out)
	}

	out = mustRun(t, "search", "PROJ-123", "--room", "dev-backend", "--room", "#dev-backend", "--room", "@walkerjj", "--json")
	var v struct {
		SearchText string `json:"searchText"`
		Rooms      []struct {
			ID       string           `json:"id"`
			Messages []map[string]any `json:"messages"`
		} `json:"rooms"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.SearchText != "/PROJ-123/i" || len(v.Rooms) != 2 || v.Rooms[0].Messages[0]["tmid"] != "b10" {
		t.Errorf("--json: %v %+v\n%s", err, v, out)
	}

	for _, args := range [][]string{{"search", "x"}, {"search", "--all"}, {"search", "x", "--all", "--room", "R1"}, {"search", "x", "--all", "--limit", "0"}} {
		if _, _, code := runRct(t, args...); code != cli.ExitUsage {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}

func TestContextInRoom(t *testing.T) {
	f := testenv.Setup(t)
	out := mustRun(t, "context", f.URL+"/channel/dev-backend?msg=b15", "--before", "3", "--after", "2")
	testenv.MustContain(t, out,
		"# #dev-backend: around b15\nURL: "+f.URL+"/channel/dev-backend?msg=b15\nPeople: ivanovii = Ivanov Ivan Ivanovich; petrovpp",
		"- 2026-10-28 10:13 walkerjj [b13]: backend message 13\n- 2026-10-28 10:14 petrovpp [b14]: backend message 14\n- 2026-10-28 10:14 ivanovii [r3] · reply in thread b10: Agreed",
		"→ 2026-10-28 10:15 ivanovii [b15]: see the log\n  [file: server.log, text/plain, id F1]\n  > walkerjj: Deploy of PROJ-123 failed on staging\n- 2026-10-28 10:16",
		"[b17]: backend message 17\n\nearlier: rct context b13 --before 20 --after 0\nlater:   rct context b17 --before 0 --after 20\n",
	)
	// System messages and replies kept in their thread stay out.
	out = mustRun(t, "context", "b13", "--before", "4", "--after", "0")
	testenv.MustContain(t, out, "[b09]", "[b10] · thread: 3 replies", "[b11]", "earlier: rct context b08")
	testenv.MustNotContain(t, out, "[b12]", "[r1]", "[r2]", "later:", "thread:  rct")
	testenv.MustContain(t, mustRun(t, "context", "b10", "--before", "0", "--after", "0"), "thread:  rct thread b10 (3 replies)")
	testenv.MustNotContain(t, mustRun(t, "context", "b10", "--before", "0", "--after", "0"), "earlier:", "later:")

	out = mustRun(t, "context", "b28", "--after", "5")
	testenv.MustContain(t, out, "[b29]", "[b30]")
	testenv.MustNotContain(t, out, "later:")
}

func TestContextWindows(t *testing.T) {
	f := testenv.Setup(t)
	// #dev-frontend has 250 messages a minute apart: the first window holds
	// more than a page, so rct narrows it before taking the next 20.
	out := mustRun(t, "context", "f001", "--before", "5")
	if strings.Count(out, "\n- ") != 20 || !strings.Contains(out, "→ 2026-10-20 10:01 walkerjj [f001]") || !strings.Contains(out, "[f021]: frontend message 21\n\nlater:   rct context f021") {
		t.Errorf("after f001:\n%s", out)
	}
	testenv.MustNotContain(t, out, "earlier:")
	if n := f.Count("/api/v1/channels.history"); n != 3 {
		t.Errorf("history requests = %d, want 1 before + 2 after", n)
	}
	out = mustRun(t, "context", "f200", "--before", "100", "--after", "100")
	testenv.MustContain(t, out, "[f100]", "earlier: rct context f100 --before 100", "[f250]")
	testenv.MustNotContain(t, out, "[f099]", "later:")
	for _, args := range [][]string{{"context"}, {"context", "a", "b"}, {"context", "b01", "--after", "101"}, {"context", "no such id"}} {
		if _, _, code := runRct(t, args...); code != cli.ExitUsage {
			t.Errorf("%v: exit %d", args, code)
		}
	}
	if _, errs, code := runRct(t, "context", "missing"); code != cli.ExitNotFound || !strings.Contains(errs, "message missing not found") {
		t.Errorf("missing: exit %d, %s", code, errs)
	}
	if _, _, code := runRct(t, "context", f.URL+"/channel/dev-backend"); code != cli.ExitUsage {
		t.Errorf("link without a message: exit %d", code)
	}
}

func TestContextInThread(t *testing.T) {
	f := testenv.Setup(t)
	out := mustRun(t, "context", f.URL+"/channel/dev-backend/thread/b10?msg=r2", "--before", "1", "--after", "0")
	testenv.MustContain(t, out,
		"# Thread b10 in #dev-backend: around r2",
		"Thread root: 2026-10-28 10:10 walkerjj [b10] · thread: 3 replies: Release plan for 2.3\n- 2026-10-28 10:10 walkerjj [r1]: PROJ-123",
		"→ 2026-10-28 10:12 petrovpp [r2]: let's postpone\n\nlater:   rct context r2 --before 0 --after 20\nthread:  rct thread b10",
	)
	testenv.MustNotContain(t, out, "reply in thread", "[r3]")
	out = mustRun(t, "context", "r3", "--json")
	var v struct {
		Message    map[string]any   `json:"message"`
		Before     []map[string]any `json:"before"`
		ThreadRoot map[string]any   `json:"threadRoot"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.Message["_id"] != "r3" || len(v.Before) != 2 || v.ThreadRoot["_id"] != "b10" {
		t.Errorf("--json: %v\n%s", err, out)
	}
}

func TestThread(t *testing.T) {
	testenv.Setup(t)
	out := mustRun(t, "thread", "r1")
	testenv.MustContain(t, out,
		"# Thread b10 in #dev-backend",
		"Root: 2026-10-28 10:10 walkerjj [b10] · thread: 3 replies: Release plan for 2.3\n\n## Replies (3)\n\n- 2026-10-28 10:10 walkerjj [r1]",
		"- 2026-10-28 10:14 ivanovii [r3]: Agreed",
	)
	out = mustRun(t, "thread", "b10", "--limit", "2")
	testenv.MustContain(t, out, "showing the first 2 of 3; raise --limit, or rct context r2 --before 0 --after 100")
	testenv.MustContain(t, mustRun(t, "thread", "b05"), "no replies; this message did not start a thread")

	out = mustRun(t, "threads", "dev-backend")
	testenv.MustContain(t, out, "# Threads in #dev-backend (1 of 1, most recent reply first)", "[b10] · thread: 3 replies, last 2026-10-28 10:14: Release plan")
	testenv.MustContain(t, mustRun(t, "threads", "dev-backend", "--text", "nothing"), `no threads in #dev-backend with "nothing"`)
	testenv.MustContain(t, mustRun(t, "threads", "@walkerjj"), "no threads in @walkerjj — Walker John Johnson")
}

func TestHistory(t *testing.T) {
	f := testenv.Setup(t)
	out := mustRun(t, "history", "dev-backend", "--limit", "5")
	testenv.MustContain(t, out,
		"# #dev-backend: 5 messages\nURL: "+f.URL+"/channel/dev-backend\n",
		"- 2026-10-28 10:26 petrovpp [b26]",
		"- 2026-10-28 10:30 ivanovii [b30]: backend message 30\n\nolder: rct context b26 --before 50 --after 0",
	)
	out = mustRun(t, "history", "dev-backend", "--since", "2026-10-28")
	testenv.MustContain(t, out, "# #dev-backend: 30 messages since 2026-10-28 00:00", "  line two")
	testenv.MustNotContain(t, out, "older:", "[r1]", "[b12]")

	mustRun(t, "history", "Release Team")
	for _, r := range f.Requests() {
		if strings.HasPrefix(r, "/api/v1/groups.history") && !strings.Contains(r, "showThreadMessages=false") {
			t.Errorf("groups.history shows thread replies by default; rct must ask it not to: %s", r)
		}
	}
	testenv.MustContain(t, mustRun(t, "history", "@petrovpp"), "no messages in @petrovpp — Petrov Petr Petrovich")
	testenv.MustContain(t, mustRun(t, "history", "@walkerjj", "--since", "1h"), "no messages in @walkerjj — Walker John Johnson since ")
	if f.Count("/api/v1/im.history") != 2 {
		t.Errorf("DMs should be read with im.history: %v", f.Requests())
	}
	if _, _, code := runRct(t, "history"); code != cli.ExitUsage {
		t.Errorf("no room: exit %d", code)
	}
	out = mustRun(t, "history", "R1", "--limit", "2", "--json")
	var v struct {
		Room     map[string]string `json:"room"`
		Messages []map[string]any  `json:"messages"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.Room["type"] != "channel" || len(v.Messages) != 2 || v.Messages[1]["_id"] != "b30" {
		t.Errorf("--json: %v\n%s", err, out)
	}
}

func TestMessageText(t *testing.T) {
	for _, tc := range []struct {
		m    rocketchat.Message
		want string
	}{
		{rocketchat.Message{}, "(empty)"},
		{rocketchat.Message{Msg: "hi", Attachments: []rocketchat.Attachment{{Title: "Build #12", Text: "failed"}}}, "hi\n[attachment: Build #12] failed"},
		{rocketchat.Message{
			Files:       []rocketchat.File{{ID: "F1", Name: "a.png", Type: "image/png"}, {ID: "F2", Name: "b.txt"}},
			Attachments: []rocketchat.Attachment{{Title: "a.png", Description: "the bug"}, {Title: "b.txt"}},
		}, "[file: a.png, image/png, id F1]\n[file: b.txt, ?, id F2]\nthe bug"},
		{rocketchat.Message{Attachments: []rocketchat.Attachment{{MessageLink: "x", Text: "a\nb"}}}, "> ?: a\n> b"},
	} {
		if got := messageText(&tc.m); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
	var b strings.Builder
	writeMessage(&b, "-", &rocketchat.Message{ID: "x1", Msg: strings.Repeat("я", 50), TS: testenv.Base, Pinned: true, DRID: "D1", DCount: 4}, lineOpts{maxChars: 10})
	if want := "- 2026-10-28 10:00 ? [x1] · discussion D1: 4 messages · pinned: " + strings.Repeat("я", 10) + "… [cut; rct context x1 --before 0 --after 0]\n"; b.String() != want {
		t.Errorf("got %q, want %q", b.String(), want)
	}
}
