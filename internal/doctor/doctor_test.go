package doctor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RyabovNick/rct/internal/cli"
	"github.com/RyabovNick/rct/internal/testenv"
)

func runRct(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	return testenv.Run(t, map[string]testenv.Command{"doctor": Run}, args...)
}

func TestDoctorOK(t *testing.T) {
	testenv.Setup(t)
	out, errs, code := runRct(t, "doctor")
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, errs, out)
	}
	testenv.MustContain(t, out,
		"(mode 600)",
		"user_id         U1 (config file)",
		"token           set (config file)",
		"ca_bundle       unset",
		"timeout         30 (default)",
		"rate_limit_wait 60 (default)",
		"✓ rocketchat: authenticated as Ivanov Ivan Ivanovich (ivanovii, ivanovii@example.com) — Rocket.Chat 8.5.0",
	)
	if strings.Contains(out, testenv.Token) || strings.Contains(out, "  ! ") {
		t.Errorf("unexpected token or warning:\n%s", out)
	}
}

func TestDoctorRejectedToken(t *testing.T) {
	f := testenv.FakeServer(t)
	testenv.ClearEnv(t)
	t.Setenv("RCT_CONFIG", filepath.Join(t.TempDir(), "absent.ini"))
	const wrong = "wrong-secret-value"
	t.Setenv("ROCKETCHAT_URL", f.URL)
	t.Setenv("ROCKETCHAT_USER_ID", testenv.UserID)
	t.Setenv("ROCKETCHAT_TOKEN", wrong)
	out, errs, code := runRct(t, "doctor")
	if code != cli.ExitAPI {
		t.Fatalf("exit %d: %s\n%s", code, errs, out)
	}
	testenv.MustContain(t, out,
		"(not present; environment variables only)",
		"url             "+f.URL+" (ROCKETCHAT_URL)",
		"token           set (ROCKETCHAT_TOKEN)",
		"✗ rocketchat: HTTP 401 on me (You must be logged in to do this.)",
	)
	if strings.Contains(out+errs, wrong) {
		t.Errorf("the token leaked:\n%s%s", out, errs)
	}
}

func TestDoctorOldServerAndOtherUser(t *testing.T) {
	mux := http.NewServeMux()
	for path, body := range map[string]string{
		"/api/v1/me": `{"_id":"U7","username":"bot","success":true}`,
		"/api/info":  `{"version":"6.3.0","success":true}`,
	} {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, body)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	testenv.WriteConfig(t, fmt.Sprintf("[rocketchat]\nurl = %s\nuser_id = U1\ntoken = t\n", srv.URL), 0o600)
	out, _, code := runRct(t, "doctor")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	testenv.MustContain(t, out,
		"✓ rocketchat: authenticated as bot — Rocket.Chat 6.3.0",
		"! the server says you are user U7, not the configured U1",
		"! Rocket.Chat 6.3.0 is older than rct supports",
	)
}

func TestDoctorConfigProblems(t *testing.T) {
	testenv.ClearEnv(t)
	t.Setenv("RCT_CONFIG", filepath.Join(t.TempDir(), "absent.ini"))
	out, _, code := runRct(t, "doctor")
	if code != cli.ExitConfig {
		t.Errorf("nothing configured: exit %d", code)
	}
	testenv.MustContain(t, out, "- rocketchat: not configured", "Nothing is configured: set ROCKETCHAT_URL, ROCKETCHAT_USER_ID and ROCKETCHAT_TOKEN")

	testenv.WriteConfig(t, "[rocketchat]\nurl = http://127.0.0.1:1\nuser_id = U1\ntoken = secret-in-file\n", 0o644)
	out, _, code = runRct(t, "doctor")
	if code != cli.ExitConfig {
		t.Errorf("readable config: exit %d", code)
	}
	testenv.MustContain(t, out, "chmod 600", "✗ rocketchat: ")
	testenv.MustNotContain(t, out, "secret-in-file", "Nothing is configured")

	testenv.WriteConfig(t, "[rocketchat]\nurl = http://127.0.0.1:1\n", 0o600)
	out, _, code = runRct(t, "doctor")
	if code != cli.ExitConfig {
		t.Errorf("half configured: exit %d", code)
	}
	testenv.MustContain(t, out, "✗ rocketchat: rocketchat needs ROCKETCHAT_USER_ID, ROCKETCHAT_TOKEN", "token           unset")

	testenv.WriteConfig(t, "[rocketchat]\nurl = http://127.0.0.1:1\nuser_id = U1\ntoken = t\n", 0o600)
	if out, _, code := runRct(t, "doctor"); code != cli.ExitAPI || !strings.Contains(out, "✗ rocketchat: ") {
		t.Errorf("unreachable server: exit %d\n%s", code, out)
	}
	if _, _, code := runRct(t, "doctor", "extra"); code != cli.ExitUsage {
		t.Errorf("doctor with an argument: exit %d", code)
	}
}

func TestDoctorJSON(t *testing.T) {
	testenv.Setup(t)
	out, errs, code := runRct(t, "doctor", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var r report
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if !r.OK || !r.ConfigFile.Exists || r.ConfigFile.Mode != "600" || r.Version != "8.5.0" || r.User == nil || r.User.Username != "ivanovii" || r.Exit != 0 {
		t.Errorf("report = %+v", r)
	}
	if r.Settings[2].Key != "token" || r.Settings[2].Value != "set" || strings.Contains(out, testenv.Token) {
		t.Errorf("the token must be reported only as set:\n%s", out)
	}
}
