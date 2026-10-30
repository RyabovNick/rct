package rocketchat

import (
	"encoding/json"
	"time"

	"github.com/RyabovNick/rct/internal/cli"
)

// User is a message author or a directory entry: username is the login
// (walkerjj), name the full name (Walker John Johnson).
type User struct {
	ID       string `json:"_id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Nickname string `json:"nickname"`
	Emails   []struct {
		Address string `json:"address"`
	} `json:"emails"`
}

// File is an uploaded file attached to a message.
type File struct {
	ID   string `json:"_id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// Attachment is a quote of another message, a file preview or an integration card.
type Attachment struct {
	Title       string `json:"title"`
	Text        string `json:"text"`
	Description string `json:"description"`
	AuthorName  string `json:"author_name"`
	MessageLink string `json:"message_link"`
	ImageURL    string `json:"image_url"`
}

// Message is a chat message, a thread root or a thread reply.
type Message struct {
	ID          string       `json:"_id"`
	RID         string       `json:"rid"`
	Msg         string       `json:"msg"`
	TS          time.Time    `json:"ts"`
	U           User         `json:"u"`
	T           string       `json:"t"`     // set on system messages: uj (joined), ru (removed), …
	TMID        string       `json:"tmid"`  // the thread root, on a reply
	TShow       bool         `json:"tshow"` // a reply also sent to the room
	TCount      int          `json:"tcount"`
	TLM         *time.Time   `json:"tlm"`
	DRID        string       `json:"drid"` // a discussion started from this message
	DCount      int          `json:"dcount"`
	Pinned      bool         `json:"pinned"`
	EditedAt    *time.Time   `json:"editedAt"`
	Attachments []Attachment `json:"attachments"`
	File        *File        `json:"file"`
	Files       []File       `json:"files"`

	// Raw is the message as the API sent it, for --json.
	Raw json.RawMessage `json:"-"`
}

// DecodeMessages decodes API messages and keeps each one's raw JSON.
func DecodeMessages(raws []json.RawMessage) ([]Message, error) {
	out := make([]Message, len(raws))
	for i, r := range raws {
		if err := json.Unmarshal(r, &out[i]); err != nil {
			return nil, cli.Fail(cli.ExitAPI, "decoding a message: %v", err)
		}
		out[i].Raw = r
	}
	return out, nil
}

// RawMessages returns the raw JSON of msgs, for --json.
func RawMessages(msgs []Message) []json.RawMessage {
	out := make([]json.RawMessage, len(msgs))
	for i, m := range msgs {
		out[i] = m.Raw
	}
	return out
}

// Subscription is the user's membership in a room. For a direct message, name
// is the other user's username and fname their full name.
type Subscription struct {
	RID          string `json:"rid"`
	Name         string `json:"name"`
	FName        string `json:"fname"`
	T            string `json:"t"`
	Open         bool   `json:"open"`
	Archived     bool   `json:"archived"`
	Unread       int    `json:"unread"`
	UserMentions int    `json:"userMentions"`
	PRID         string `json:"prid"`
	TeamMain     bool   `json:"teamMain"`
}

// Room is a channel (t=c), private group (p), direct message (d) or livechat
// (l). A discussion is a c or p room with a parent, prid.
type Room struct {
	ID         string     `json:"_id"`
	T          string     `json:"t"`
	Name       string     `json:"name"`
	FName      string     `json:"fname"`
	Topic      string     `json:"topic"`
	UsersCount int        `json:"usersCount"`
	Msgs       int        `json:"msgs"`
	LM         *time.Time `json:"lm"`
	PRID       string     `json:"prid"`
	TeamMain   bool       `json:"teamMain"`
	Archived   bool       `json:"archived"`
	Usernames  []string   `json:"usernames"`
	// LastMessage comes with directory results, which have no lm.
	LastMessage *struct {
		TS time.Time `json:"ts"`
	} `json:"lastMessage"`
}

// LastActivity is when the room last had a message, zero if unknown.
func (r *Room) LastActivity() time.Time {
	switch {
	case r.LM != nil:
		return *r.LM
	case r.LastMessage != nil:
		return r.LastMessage.TS
	}
	return time.Time{}
}
