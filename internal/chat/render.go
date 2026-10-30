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

// stamp is a message time in local time, to the minute.
func stamp(t time.Time) string { return t.Local().Format("2006-01-02 15:04") }

// people maps the usernames in a listing to full names, so that lines can
// show the short username and agents can still match people to Jira.
type people map[string]string

func (p people) add(msgs ...rocketchat.Message) {
	for _, m := range msgs {
		if m.U.Username != "" && p[m.U.Username] == "" {
			p[m.U.Username] = m.U.Name
		}
	}
}

func (p people) write(w io.Writer) {
	if len(p) == 0 {
		return
	}
	names := make([]string, 0, len(p))
	for u := range p {
		names = append(names, u)
	}
	slices.Sort(names)
	for i, u := range names {
		if full := p[u]; full != "" && full != u {
			names[i] = u + " = " + full
		}
	}
	fmt.Fprintf(w, "People: %s\n", strings.Join(names, "; "))
}

type lineOpts struct {
	maxChars  int  // 0 prints the whole text
	inThread  bool // replies need no "reply in thread" note
	lastReply bool // note when a thread root last got a reply
}

// writeMessage prints one message: prefix, time, author, [id], notes, then
// the text, with continuation lines indented.
func writeMessage(w io.Writer, prefix string, m *rocketchat.Message, o lineOpts) {
	var notes []string
	if m.TMID != "" && !o.inThread {
		notes = append(notes, "reply in thread "+m.TMID)
	}
	if m.TCount > 0 {
		note := fmt.Sprintf("thread: %d %s", m.TCount, plural(m.TCount, "reply", "replies"))
		if o.lastReply && m.TLM != nil {
			note += ", last " + stamp(*m.TLM)
		}
		notes = append(notes, note)
	}
	if m.DRID != "" {
		notes = append(notes, fmt.Sprintf("discussion %s: %d messages", m.DRID, m.DCount))
	}
	if m.Pinned {
		notes = append(notes, "pinned")
	}
	if m.EditedAt != nil {
		notes = append(notes, "edited")
	}
	head := fmt.Sprintf("%s %s %s [%s]", prefix, stamp(m.TS), cli.Or(m.U.Username, "?"), m.ID)
	if len(notes) > 0 {
		head += " · " + strings.Join(notes, " · ")
	}
	text := messageText(m)
	if o.maxChars > 0 {
		if r := []rune(text); len(r) > o.maxChars {
			text = strings.TrimRight(string(r[:o.maxChars]), " \n") + fmt.Sprintf("… [cut; rct context %s --before 0 --after 0]", m.ID)
		}
	}
	lines := strings.Split(text, "\n")
	fmt.Fprintf(w, "%s: %s\n", head, lines[0])
	for _, l := range lines[1:] {
		fmt.Fprintf(w, "  %s\n", l)
	}
}

// messageText is the message body plus its quotes, files and cards.
func messageText(m *rocketchat.Message) string {
	var parts []string
	if t := strings.TrimSpace(m.Msg); t != "" {
		parts = append(parts, t)
	}
	files := m.Files
	if len(files) == 0 && m.File != nil {
		files = []rocketchat.File{*m.File}
	}
	fileNames := map[string]bool{}
	for _, f := range files {
		fileNames[f.Name] = true
		parts = append(parts, fmt.Sprintf("[file: %s, %s, id %s]", f.Name, cli.Or(f.Type, "?"), f.ID))
	}
	for _, a := range m.Attachments {
		body := strings.TrimSpace(cli.Or(a.Text, a.Description))
		switch {
		case a.MessageLink != "":
			quote := "> " + cli.Or(a.AuthorName, "?") + ": " + strings.ReplaceAll(body, "\n", "\n> ")
			parts = append(parts, quote)
		case fileNames[a.Title]:
			// The preview card of an uploaded file; only its caption is new.
			if a.Description != "" && a.Description != m.Msg {
				parts = append(parts, a.Description)
			}
		case a.Title != "" || body != "":
			card := "[attachment: " + strings.TrimSpace(a.Title) + "]"
			if body != "" {
				card += " " + body
			}
			parts = append(parts, card)
		}
	}
	if len(parts) == 0 {
		return "(empty)"
	}
	return strings.Join(parts, "\n")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// truncateRunes cuts s to n characters, marking the cut.
func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func byTime(a, b rocketchat.Message) int { return a.TS.Compare(b.TS) }
