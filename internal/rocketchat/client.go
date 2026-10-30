package rocketchat

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/RyabovNick/rct/internal/cli"
)

// Client sends authenticated read-only requests to one Rocket.Chat server.
type Client struct {
	Base    string // base URL without a trailing slash
	UserID  string
	header  http.Header
	http    *http.Client
	maxWait time.Duration
	stderr  io.Writer
}

// Sleep waits out rate limits; tests replace it.
var Sleep = time.Sleep

// retries is how many times a rate-limited request is retried.
const retries = 3

func seconds(s map[string]string, key string, def time.Duration) (time.Duration, error) {
	v := s[key]
	if v == "" {
		return def, nil
	}
	secs, err := strconv.ParseFloat(v, 64)
	if err != nil || secs < 0 {
		return 0, cli.Fail(cli.ExitConfig, "[%s] %s must be seconds, not %q", Section, key, v)
	}
	return time.Duration(secs * float64(time.Second)), nil
}

// NewClient builds a client from the settings. Rate-limit waits are noted on stderr.
func NewClient(stderr io.Writer) (*Client, error) {
	s, err := Settings()
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, key := range []string{"url", "user_id", "token"} {
		if s[key] == "" {
			missing = append(missing, EnvVars[key])
		}
	}
	if len(missing) > 0 {
		return nil, cli.Fail(cli.ExitConfig, "rocketchat needs %s: set them, or url, user_id and token under [%s] in %s",
			strings.Join(missing, ", "), Section, ConfigPath())
	}
	base := strings.TrimRight(s["url"], "/")
	if u, parseErr := url.Parse(base); parseErr != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, cli.Fail(cli.ExitConfig, "rocketchat url must be http(s)://host, not %q", s["url"])
	}
	c := &Client{
		Base:   base,
		UserID: s["user_id"],
		header: http.Header{
			"Accept":       {"application/json"},
			"X-User-Id":    {s["user_id"]},
			"X-Auth-Token": {s["token"]},
		},
		stderr: stderr,
	}
	timeout, err := seconds(s, "timeout", 30*time.Second)
	if err != nil {
		return nil, err
	}
	if c.maxWait, err = seconds(s, "rate_limit_wait", 60*time.Second); err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if bundle := s["ca_bundle"]; bundle != "" {
		pem, err := os.ReadFile(bundle)
		if err != nil {
			return nil, cli.Fail(cli.ExitConfig, "[%s] ca_bundle: %v", Section, err)
		}
		pool, _ := x509.SystemCertPool()
		if pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, cli.Fail(cli.ExitConfig, "[%s] ca_bundle %s holds no PEM certificates", Section, bundle)
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool}
	}
	c.http = &http.Client{
		Timeout:   timeout,
		Transport: transport,
		// Redirects are reported, not followed, so the token never leaves the configured host.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return c, nil
}

func (c *Client) raw(path string, params url.Values) (*http.Response, []byte, error) {
	u := c.Base + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, nil, cli.Fail(cli.ExitUsage, "rocketchat: %v", err)
	}
	req.Header = c.header.Clone()
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, cli.Fail(cli.ExitAPI, "rocketchat: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, cli.Fail(cli.ExitAPI, "rocketchat: reading %s: %v", path, err)
	}
	return resp, body, nil
}

// rateLimitWait reads how long to wait from X-RateLimit-Reset, a Unix time in
// milliseconds, which Rocket.Chat sends with every rate-limited response.
func rateLimitWait(h http.Header, now time.Time) time.Duration {
	ms, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64)
	if err != nil {
		if secs, err := strconv.Atoi(h.Get("Retry-After")); err == nil {
			return time.Duration(secs) * time.Second
		}
		return 10 * time.Second
	}
	// A little slack, so the retry lands after the window has reset.
	return max(time.UnixMilli(ms).Sub(now)+500*time.Millisecond, time.Second)
}

// Get fetches a JSON resource, decodes it into out when non-nil, and returns
// the raw body. A rate-limited request waits for the limit to reset and is
// retried, up to rate_limit_wait seconds per wait.
func (c *Client) Get(path string, params url.Values, out any) ([]byte, error) {
	endpoint := strings.TrimPrefix(path, "/api/v1/")
	for attempt := 0; ; attempt++ {
		resp, body, err := c.raw(path, params)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			wait := rateLimitWait(resp.Header, time.Now())
			if attempt >= retries || wait > c.maxWait {
				return nil, cli.Fail(cli.ExitAPI, "rocketchat: rate limited on %s; it resets in %s, try again then (rate_limit_wait is %s)",
					endpoint, wait.Round(time.Second), c.maxWait)
			}
			fmt.Fprintf(c.stderr, "rct: rate limited on %s; waiting %s\n", endpoint, wait.Round(time.Second))
			Sleep(wait)
			continue
		}
		return c.check(path, resp, body, out)
	}
}

func (c *Client) check(path string, resp *http.Response, body []byte, out any) ([]byte, error) {
	endpoint := strings.TrimPrefix(path, "/api/v1/")
	msg := errorMessage(body)
	switch code := resp.StatusCode; {
	case code >= 300 && code < 400:
		return nil, cli.Fail(cli.ExitAPI, "rocketchat: %s redirected to %s; check that %s is the server's address",
			endpoint, resp.Header.Get("Location"), EnvVars["url"])
	case code == http.StatusUnauthorized:
		return nil, cli.Fail(cli.ExitAPI, "rocketchat: HTTP 401 on %s (%s); check that %s and %s belong to the same personal access token",
			endpoint, cli.Or(msg, "unauthorized"), EnvVars["token"], EnvVars["user_id"])
	case code == http.StatusNotFound:
		return nil, cli.Fail(cli.ExitNotFound, "rocketchat: not found: %s", endpoint)
	case code == http.StatusForbidden || (code == http.StatusBadRequest && notFoundError(body)):
		return nil, cli.Fail(cli.ExitNotFound, "rocketchat: %s: %s (missing, or not visible to you)", endpoint, cli.Or(msg, "not allowed"))
	case code == http.StatusBadRequest:
		return nil, cli.Fail(cli.ExitUsage, "rocketchat: rejected %s: %s", endpoint, cli.Or(msg, cli.Truncate(string(body), 300)))
	case code >= 400:
		return nil, cli.Fail(cli.ExitAPI, "rocketchat: HTTP %d on %s: %s", code, endpoint, cli.Or(msg, cli.Truncate(string(body), 300)))
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "json") {
		return nil, cli.Fail(cli.ExitAPI, "rocketchat: %s did not return JSON; check that %s is the server's address", endpoint, EnvVars["url"])
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return nil, cli.Fail(cli.ExitAPI, "rocketchat: decoding %s: %v", endpoint, err)
		}
	}
	return body, nil
}

type apiError struct {
	Error     string `json:"error"`
	ErrorType string `json:"errorType"`
	Message   string `json:"message"`
}

// errorMessage extracts the reason from an error body: REST failures send
// error (and sometimes errorType), the auth layer sends message.
func errorMessage(body []byte) string {
	var e apiError
	if json.Unmarshal(body, &e) != nil {
		return ""
	}
	return cli.Or(e.Error, e.Message)
}

// notFoundError tells a missing or inaccessible room or message apart from a
// bad request: Rocket.Chat answers all of them with HTTP 400.
func notFoundError(body []byte) bool {
	var e apiError
	if json.Unmarshal(body, &e) != nil {
		return false
	}
	s := strings.ToLower(e.Error + " " + e.ErrorType)
	for _, k := range []string{"not-found", "not-allowed", "not allowed", "invalid-room", "invalid-message", "invalid message", "room-not"} {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}
