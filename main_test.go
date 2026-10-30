package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RyabovNick/rct/internal/cli"
	"github.com/RyabovNick/rct/internal/testenv"
)

func TestMain(m *testing.M) {
	time.Local = time.UTC
	os.Exit(m.Run())
}

func runRct(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return out.String(), errb.String(), code
}

func TestRunUsage(t *testing.T) {
	if _, errs, code := runRct(t); code != cli.ExitUsage || !strings.Contains(errs, "rct search") {
		t.Errorf("no args: exit %d, %s", code, errs)
	}
	if out, _, code := runRct(t, "--help"); code != 0 || !strings.Contains(out, "Exit codes") {
		t.Errorf("--help: exit %d, %s", code, out)
	}
	if _, errs, code := runRct(t, "frob"); code != cli.ExitUsage || !strings.Contains(errs, `unknown command "frob"`) {
		t.Errorf("unknown: exit %d, %s", code, errs)
	}
	if _, errs, code := runRct(t, "search", "-h"); code != 0 || !strings.Contains(errs, "usage: rct search") {
		t.Errorf("search -h: exit %d, %s", code, errs)
	}
	for _, args := range [][]string{{"thread"}, {"threads"}, {"search", "x"}, {"rooms", "--nope"}} {
		if _, _, code := runRct(t, args...); code != cli.ExitUsage {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}

func TestErrors(t *testing.T) {
	f := testenv.FakeServer(t)
	cfg := fmt.Sprintf("[rocketchat]\nurl = %s\nuser_id = %s\ntoken = wrong\n", f.URL, testenv.UserID)

	testenv.WriteConfig(t, cfg, 0o644)
	if _, errs, code := runRct(t, "rooms"); code != cli.ExitConfig || !strings.Contains(errs, "chmod 600") {
		t.Errorf("readable config: exit %d, %s", code, errs)
	}
	testenv.WriteConfig(t, cfg, 0o600)
	if _, errs, code := runRct(t, "rooms"); code != cli.ExitAPI || !strings.Contains(errs, "rct: rocketchat: HTTP 401") {
		t.Errorf("bad token: exit %d, %s", code, errs)
	}
	testenv.Setup(t)
	if _, errs, code := runRct(t, "thread", "nope"); code != cli.ExitNotFound || strings.Count(errs, "rct:") != 1 {
		t.Errorf("missing message: exit %d, %s", code, errs)
	}
}

func TestEnvOnly(t *testing.T) {
	f := testenv.FakeServer(t)
	testenv.ClearEnv(t)
	t.Setenv("RCT_CONFIG", filepath.Join(t.TempDir(), "absent.ini"))
	t.Setenv("ROCKETCHAT_URL", f.URL)
	t.Setenv("ROCKETCHAT_USER_ID", testenv.UserID)
	t.Setenv("ROCKETCHAT_TOKEN", testenv.Token)
	out, errs, code := runRct(t, "search", "PROJ-123", "--room", "@walkerjj")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	testenv.MustContain(t, out, "[d1]: PROJ-123: can you check the migration?")

	t.Setenv("ROCKETCHAT_TOKEN", "")
	if _, errs, code := runRct(t, "rooms"); code != cli.ExitConfig || !strings.Contains(errs, "ROCKETCHAT_TOKEN") {
		t.Errorf("missing token: exit %d, %s", code, errs)
	}
}

func TestVersion(t *testing.T) {
	old := cli.Version
	cli.Version = "v1.2.3"
	t.Cleanup(func() { cli.Version = old })
	for _, arg := range []string{"version", "--version"} {
		if out, _, code := runRct(t, arg); code != 0 || out != "rct v1.2.3\n" {
			t.Errorf("%s: exit %d, %q", arg, code, out)
		}
	}
}

func TestJSONOutput(t *testing.T) {
	testenv.Setup(t)
	for _, args := range [][]string{
		{"rooms", "--json"},
		{"rooms", "--public", "--json"},
		{"users", "--json"},
		{"search", "release", "--room", "R1", "--json"},
		{"context", "b10", "--json"},
		{"thread", "b10", "--json"},
		{"threads", "R1", "--json"},
		{"history", "R1", "--json"},
		{"doctor", "--json"},
	} {
		out, errs, code := runRct(t, args...)
		if code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, errs)
		}
		var v any
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Errorf("%v: not JSON: %v\n%s", args, err, out)
		}
	}
}
