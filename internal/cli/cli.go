// Package cli holds what every rct command shares: an error that carries the
// exit code, flag parsing, and output helpers.
package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
)

// Exit codes.
const (
	ExitAPI      = 1
	ExitUsage    = 2
	ExitConfig   = 3
	ExitNotFound = 4
)

// Error is a failure with the exit code rct should end with.
type Error struct {
	Msg  string
	Code int
}

func (e *Error) Error() string { return e.Msg }

// Fail returns an *Error with a formatted message.
func Fail(code int, format string, args ...any) error {
	return &Error{Msg: fmt.Sprintf(format, args...), Code: code}
}

// Code returns the exit code err carries, ExitAPI for a plain error and 0 for nil.
func Code(err error) int {
	if err == nil {
		return 0
	}
	if e, ok := errors.AsType[*Error](err); ok {
		return e.Code
	}
	return ExitAPI
}

// Report prints err, if any, and returns the exit code for it.
func Report(err error, stderr io.Writer) int {
	if err == nil {
		return 0
	}
	if e, ok := errors.AsType[*Error](err); ok {
		if e.Msg != "" {
			fmt.Fprintf(stderr, "rct: %s\n", e.Msg)
		}
		return e.Code
	}
	fmt.Fprintf(stderr, "rct: %v\n", err)
	return ExitAPI
}

// NewFlags returns a flag set whose -h prints the command's usage to stderr.
func NewFlags(name, args, help string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: rct %s %s\n\n%s\n\n", name, args, help)
		fs.PrintDefaults()
	}
	return fs
}

// ParseFlags allows flags after positional arguments, as in `rct thread ID --limit 5`.
func ParseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, &Error{Code: 0}
			}
			return nil, &Error{Msg: err.Error(), Code: ExitUsage}
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

// Strings is a repeatable string flag, as in --room a --room b.
type Strings []string

func (s *Strings) String() string { return fmt.Sprint([]string(*s)) }

// Set appends one value.
func (s *Strings) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// WriteJSON prints raw JSON indented.
func WriteJSON(w io.Writer, raw []byte) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return Fail(ExitAPI, "indenting JSON: %v", err)
	}
	fmt.Fprintln(w, buf.String())
	return nil
}

// MarshalJSON encodes v and prints it indented.
func MarshalJSON(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return Fail(ExitAPI, "encoding JSON: %v", err)
	}
	return WriteJSON(w, data)
}

// Or returns s, or fallback when s is empty.
func Or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// Truncate cuts s to at most n bytes.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
