// Package rocketchat reads the rct configuration, talks to the Rocket.Chat
// REST API (v1, as of 8.5), and holds the API types rct decodes.
package rocketchat

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/RyabovNick/rct/internal/cli"
)

// Section is the config file section rct reads.
const Section = "rocketchat"

// EnvVars maps a setting to the variable that overrides it.
var EnvVars = map[string]string{
	"url":     "ROCKETCHAT_URL",
	"user_id": "ROCKETCHAT_USER_ID",
	"token":   "ROCKETCHAT_TOKEN",
}

// CredentialVars names the variables a user has to set.
const CredentialVars = "ROCKETCHAT_URL, ROCKETCHAT_USER_ID and ROCKETCHAT_TOKEN" //nolint:gosec // variable names, not a credential

// ConfigPath is $RCT_CONFIG, or ~/.config/rct/config.ini.
func ConfigPath() string {
	if p := os.Getenv("RCT_CONFIG"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "rct", "config.ini")
}

func parseINI(data string) (map[string]map[string]string, error) {
	cfg := map[string]map[string]string{}
	var cur map[string]string
	for i, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			cur = map[string]string{}
			cfg[strings.TrimSpace(line[1:len(line)-1])] = cur
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || cur == nil {
			return nil, fmt.Errorf("line %d: expected key = value inside a [section]", i+1)
		}
		cur[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return cfg, nil
}

// LoadConfig reads the optional config file; a missing file is an empty config.
func LoadConfig() (map[string]map[string]string, error) {
	path := ConfigPath()
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]map[string]string{}, nil
	}
	if err != nil {
		return nil, cli.Fail(cli.ExitConfig, "%v", err)
	}
	// The file holds a token; refusing a readable one is cheaper than leaking it.
	if info.Mode().Perm()&0o077 != 0 {
		return nil, cli.Fail(cli.ExitConfig, "%s is accessible to other users; chmod 600 it", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, cli.Fail(cli.ExitConfig, "%v", err)
	}
	cfg, err := parseINI(string(data))
	if err != nil {
		return nil, cli.Fail(cli.ExitConfig, "%s: %v", path, err)
	}
	return cfg, nil
}

// Settings is the [rocketchat] section with the environment variables on top.
func Settings() (map[string]string, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	s := map[string]string{}
	maps.Copy(s, cfg[Section])
	for key, env := range EnvVars {
		if v := os.Getenv(env); v != "" {
			s[key] = v
		}
	}
	return s, nil
}
