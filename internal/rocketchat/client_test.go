package rocketchat

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RyabovNick/rct/internal/cli"
)

func configure(t *testing.T, body string, mode os.FileMode) {
	t.Helper()
	for _, v := range EnvVars {
		t.Setenv(v, "")
	}
	path := filepath.Join(t.TempDir(), "config.ini")
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RCT_CONFIG", path)
}

func TestConfig(t *testing.T) {
	configure(t, "[rocketchat]\nurl = https://chat.example.com/\nuser_id = U1\ntoken = secret\n", 0o644)
	if _, err := NewClient(io.Discard); cli.Code(err) != cli.ExitConfig || !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("readable config: %v", err)
	}
	configure(t, "[rocketchat]\nurl = https://chat.example.com/\n", 0o600)
	_, err := NewClient(io.Discard)
	if cli.Code(err) != cli.ExitConfig || !strings.Contains(err.Error(), "needs ROCKETCHAT_USER_ID, ROCKETCHAT_TOKEN") {
		t.Errorf("missing values: %v", err)
	}
	t.Setenv("ROCKETCHAT_USER_ID", "U9")
	t.Setenv("ROCKETCHAT_TOKEN", "tok")
	c, err := NewClient(io.Discard)
	if err != nil || c.Base != "https://chat.example.com" || c.UserID != "U9" || c.header.Get("X-Auth-Token") != "tok" {
		t.Errorf("env over file: %+v, %v", c, err)
	}
	for body, want := range map[string]string{
		"[rocketchat]\nurl = chat.example.com\nuser_id = U\ntoken = t\n":                        "must be http(s)://host",
		"[rocketchat]\nurl = https://x\nuser_id = U\ntoken = t\ntimeout = soon\n":               "timeout must be seconds",
		"[rocketchat]\nurl = https://x\nuser_id = U\ntoken = t\nca_bundle = /nonexistent.pem\n": "ca_bundle",
		"url = https://x\n": "line 1: expected key = value inside a [section]",
	} {
		configure(t, body, 0o600)
		if _, err := NewClient(io.Discard); cli.Code(err) != cli.ExitConfig || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v", body, err)
		}
	}
}

func TestStatusMapping(t *testing.T) {
	mux := http.NewServeMux()
	reply := func(path string, code int, ctype, body string, headers ...string) {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			for i := 0; i+1 < len(headers); i += 2 {
				w.Header().Set(headers[i], headers[i+1])
			}
			w.Header().Set("Content-Type", ctype)
			w.WriteHeader(code)
			fmt.Fprint(w, body)
		})
	}
	const j = "application/json"
	reply("/unauth", 401, j, `{"status":"error","message":"You must be logged in to do this."}`)
	reply("/forbidden", 403, j, `{"success":false,"error":"unauthorized"}`)
	reply("/missing", 404, j, `{"success":false,"error":"Resource not found"}`)
	reply("/noroom", 400, j, `{"success":false,"error":"not-allowed","errorType":"Not Allowed"}`)
	reply("/bad", 400, j, `{"success":false,"error":"Invalid sort parameter provided"}`)
	reply("/boom", 500, j, `{"success":false,"error":"oops"}`)
	reply("/moved", 302, "text/html", ``, "Location", "https://sso.example.com/login")
	reply("/html", 200, "text/html", `<html>`)
	reply("/slow", 429, j, `{"success":false}`, "X-RateLimit-Reset", fmt.Sprint(time.Now().Add(10*time.Minute).UnixMilli()))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	configure(t, fmt.Sprintf("[rocketchat]\nurl = %s\nuser_id = U1\ntoken = t\n", srv.URL), 0o600)
	c, err := NewClient(io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]struct {
		code int
		msg  string
	}{
		"/unauth":    {cli.ExitAPI, "HTTP 401 on /unauth (You must be logged in to do this.); check that ROCKETCHAT_TOKEN and ROCKETCHAT_USER_ID"},
		"/forbidden": {cli.ExitNotFound, "not visible to you"},
		"/missing":   {cli.ExitNotFound, "not found"},
		"/noroom":    {cli.ExitNotFound, "not-allowed (missing, or not visible to you)"},
		"/bad":       {cli.ExitUsage, "rejected /bad: Invalid sort parameter provided"},
		"/boom":      {cli.ExitAPI, "HTTP 500 on /boom: oops"},
		"/moved":     {cli.ExitAPI, "redirected to https://sso.example.com/login"},
		"/html":      {cli.ExitAPI, "did not return JSON"},
		"/slow":      {cli.ExitAPI, "rate limited on /slow; it resets in 10m"},
	} {
		_, err := c.Get(path, nil, nil)
		if cli.Code(err) != want.code || !strings.Contains(fmt.Sprint(err), want.msg) {
			t.Errorf("%s: %v (exit %d), want %d and %q", path, err, cli.Code(err), want.code, want.msg)
		}
	}
}

func TestRateLimitRetries(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Add(3*time.Second).UnixMilli()))
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"success":false,"error":"Error, too many requests."}`)
	}))
	t.Cleanup(srv.Close)
	configure(t, fmt.Sprintf("[rocketchat]\nurl = %s\nuser_id = U1\ntoken = t\n", srv.URL), 0o600)
	var waits []time.Duration
	old := Sleep
	Sleep = func(d time.Duration) { waits = append(waits, d) }
	t.Cleanup(func() { Sleep = old })
	var stderr strings.Builder
	c, _ := NewClient(&stderr)
	_, err := c.Get("/api/v1/chat.search", nil, nil)
	if calls != retries+1 || len(waits) != retries || cli.Code(err) != cli.ExitAPI || !strings.Contains(err.Error(), "rate limited on chat.search") {
		t.Errorf("calls %d, waits %v, err %v", calls, waits, err)
	}
	for _, w := range waits {
		if w < 2*time.Second || w > 4*time.Second {
			t.Errorf("wait %v, want about 3.5s", w)
		}
	}
	if strings.Count(stderr.String(), "rct: rate limited on chat.search; waiting") != retries {
		t.Errorf("stderr:\n%s", stderr.String())
	}

	now := time.Now()
	for h, want := range map[string]time.Duration{"Retry-After=7": 7 * time.Second, "": 10 * time.Second} {
		hdr := http.Header{}
		if k, v, ok := strings.Cut(h, "="); ok {
			hdr.Set(k, v)
		}
		if got := rateLimitWait(hdr, now); got != want {
			t.Errorf("%q: %v, want %v", h, got, want)
		}
	}
}
