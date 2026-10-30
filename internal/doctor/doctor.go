// Package doctor implements `rct doctor`: where each setting comes from, and
// whether Rocket.Chat accepts the configured token.
package doctor

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"github.com/RyabovNick/rct/internal/cli"
	"github.com/RyabovNick/rct/internal/rocketchat"
)

type setting struct {
	Key    string `json:"key"`
	Value  string `json:"value,omitempty"`
	Source string `json:"source"`
}

type fileStatus struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Mode   string `json:"mode,omitempty"`
	Error  string `json:"error,omitempty"`
}

type user struct {
	ID       string `json:"id,omitempty"`
	Username string `json:"username,omitempty"`
	Name     string `json:"name,omitempty"`
	Email    string `json:"email,omitempty"`
}

type report struct {
	ConfigFile fileStatus `json:"config_file"`
	Configured bool       `json:"configured"`
	Settings   []setting  `json:"settings"`
	OK         bool       `json:"ok"`
	Error      string     `json:"error,omitempty"`
	User       *user      `json:"user,omitempty"`
	Version    string     `json:"version,omitempty"`
	Warnings   []string   `json:"warnings,omitempty"`
	Exit       int        `json:"exit"`

	// reached is false when the check stopped at configuration, before any request.
	reached bool
}

func inspectConfigFile() (fileStatus, map[string]string) {
	st := fileStatus{Path: rocketchat.ConfigPath()}
	info, err := os.Stat(st.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	st.Exists = true
	if err == nil {
		st.Mode = fmt.Sprintf("%o", info.Mode().Perm())
	}
	cfg, err := rocketchat.LoadConfig()
	if err != nil {
		st.Error = err.Error()
		return st, nil
	}
	return st, cfg[rocketchat.Section]
}

// describeSettings reports each effective value and where it came from. The
// token's value is never included, only whether it is set.
func describeSettings(file map[string]string) []setting {
	defaults := map[string]string{"timeout": "30", "rate_limit_wait": "60"}
	var out []setting
	for _, key := range []string{"url", "user_id", "token", "ca_bundle", "timeout", "rate_limit_wait"} {
		s := setting{Key: key, Source: "unset"}
		if env := rocketchat.EnvVars[key]; env != "" && os.Getenv(env) != "" {
			s.Value, s.Source = os.Getenv(env), env
		} else if file[key] != "" {
			s.Value, s.Source = file[key], "config file"
		} else if d := defaults[key]; d != "" {
			s.Value, s.Source = d, "default"
		}
		if key == "token" && s.Source != "unset" {
			s.Value = "set"
		}
		out = append(out, s)
	}
	return out
}

// minMajor is the oldest Rocket.Chat release rct's endpoints are checked against.
const minMajor = 7

func check(c *rocketchat.Client, r *report) {
	var me struct {
		ID       string `json:"_id"`
		Username string `json:"username"`
		Name     string `json:"name"`
		Emails   []struct {
			Address string `json:"address"`
		} `json:"emails"`
	}
	if _, err := c.Get("/api/v1/me", nil, &me); err != nil {
		r.Error = err.Error()
		return
	}
	r.OK = true
	r.User = &user{ID: me.ID, Username: me.Username, Name: me.Name}
	if len(me.Emails) > 0 {
		r.User.Email = me.Emails[0].Address
	}
	if me.ID != "" && me.ID != c.UserID {
		r.Warnings = append(r.Warnings, fmt.Sprintf("the server says you are user %s, not the configured %s", me.ID, c.UserID))
	}

	// Recent servers report the version only to authenticated users, as info.version.
	var info struct {
		Version string `json:"version"`
		Info    struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if _, err := c.Get("/api/info", nil, &info); err != nil {
		r.Warnings = append(r.Warnings, "server version unavailable: "+err.Error())
		return
	}
	r.Version = cli.Or(info.Info.Version, info.Version)
	major, _, _ := strings.Cut(r.Version, ".")
	if n, err := strconv.Atoi(major); err == nil && n < minMajor {
		r.Warnings = append(r.Warnings, fmt.Sprintf("Rocket.Chat %s is older than rct supports (%d.0 and later; built against 8.5)", r.Version, minMajor))
	}
}

func diagnose(stderr io.Writer) report {
	file, cfg := inspectConfigFile()
	r := report{ConfigFile: file, Settings: describeSettings(cfg)}
	for _, s := range r.Settings {
		if (s.Key == "url" || s.Key == "user_id" || s.Key == "token") && s.Source != "unset" {
			r.Configured = true
		}
	}
	switch {
	case file.Error != "":
		r.Error = file.Error
	case r.Configured:
		c, err := rocketchat.NewClient(stderr)
		if err != nil {
			r.Error = err.Error()
			break
		}
		r.reached = true
		check(c, &r)
	}
	switch {
	case !r.reached:
		r.Exit = cli.ExitConfig
	case r.Error != "":
		r.Exit = cli.ExitAPI
	}
	return r
}

func (r report) summary() string {
	switch {
	case r.OK:
		who := cli.Or(r.User.Name, r.User.Username)
		var ids []string
		if r.User.Username != "" && r.User.Username != who {
			ids = append(ids, r.User.Username)
		}
		if r.User.Email != "" {
			ids = append(ids, r.User.Email)
		}
		if len(ids) > 0 {
			who += " (" + strings.Join(ids, ", ") + ")"
		}
		line := "✓ rocketchat: authenticated as " + who
		if r.Version != "" {
			line += " — Rocket.Chat " + r.Version
		}
		return line
	case r.Error != "":
		return "✗ rocketchat: " + strings.TrimPrefix(r.Error, "rocketchat: ")
	default:
		return "- rocketchat: not configured"
	}
}

// Run implements `rct doctor`.
func Run(args []string, stdout, stderr io.Writer) error {
	fs := cli.NewFlags("doctor", "", "Check rct's configuration and that Rocket.Chat accepts the token.\nThe token is never printed.", stderr)
	asJSON := fs.Bool("json", false, "print the report as JSON")
	pos, err := cli.ParseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return cli.Fail(cli.ExitUsage, "doctor takes no arguments")
	}
	r := diagnose(stderr)
	if *asJSON {
		data, err := json.Marshal(r)
		if err != nil {
			return cli.Fail(cli.ExitAPI, "encoding JSON: %v", err)
		}
		if err := cli.WriteJSON(stdout, data); err != nil {
			return err
		}
	} else if err := write(stdout, r); err != nil {
		return err
	}
	if r.Exit != 0 {
		return &cli.Error{Code: r.Exit}
	}
	return nil
}

func write(w io.Writer, r report) error {
	var b strings.Builder
	switch f := r.ConfigFile; {
	case f.Error != "":
		fmt.Fprintf(&b, "Config file: %s (%s)\n", f.Path, f.Error)
	case f.Exists:
		fmt.Fprintf(&b, "Config file: %s (mode %s)\n", f.Path, f.Mode)
	default:
		fmt.Fprintf(&b, "Config file: %s (not present; environment variables only)\n", f.Path)
	}
	fmt.Fprintf(&b, "\nrocketchat\n")
	for _, s := range r.Settings {
		if s.Source == "unset" {
			fmt.Fprintf(&b, "  %-15s unset\n", s.Key)
			continue
		}
		fmt.Fprintf(&b, "  %-15s %s (%s)\n", s.Key, s.Value, s.Source)
	}
	fmt.Fprintf(&b, "%s\n", r.summary())
	for _, warn := range r.Warnings {
		fmt.Fprintf(&b, "  ! %s\n", warn)
	}
	if !r.Configured && r.ConfigFile.Error == "" {
		fmt.Fprintf(&b, "\nNothing is configured: set %s, or create %s.\n", rocketchat.CredentialVars, r.ConfigFile.Path)
	}
	_, err := io.WriteString(w, b.String())
	return err
}
