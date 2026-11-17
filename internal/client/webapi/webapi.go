// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

// Package webapi implements the client.Transport interface on top of the JSON
// API that the EdgeOS web UI uses.
//
// Login is a form POST to "/" that sets the PHPSESSID and X-CSRF-TOKEN
// cookies. Reads use /api/edge/partial.json, writes use /api/edge/batch.json,
// which applies DELETE then SET, commits and saves in one request.
package webapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/client"
	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

const (
	sessionCookie = "PHPSESSID"
	csrfCookie    = "X-CSRF-TOKEN"
	csrfHeader    = "X-CSRF-TOKEN"
)

// Config holds the connection settings.
type Config struct {
	// Host is a host name or IP address, optionally with a port.
	Host string
	// Port overrides the HTTPS port. Zero means 443 or the port in Host.
	Port     int
	Username string
	Password string
	// Insecure skips TLS certificate verification. EdgeRouters ship with a
	// self-signed certificate.
	Insecure bool
	// CACertPEM, if set, is the only CA trusted for the router certificate.
	CACertPEM []byte
	// Timeout applies to each HTTP request. Zero means 60 seconds.
	Timeout time.Duration
	// UserAgent is sent with every request.
	UserAgent string
	// Logf, if set, receives debug messages. Passwords are never logged.
	Logf func(format string, args ...any)
}

// Transport is the web API transport. It is safe for concurrent use.
type Transport struct {
	cfg  Config
	base *url.URL
	hc   *http.Client

	mu       sync.Mutex
	loggedIn bool
	logins   int
}

// New validates the config and returns a transport. It does not contact the
// device; the first request logs in.
func New(cfg Config) (*Transport, error) {
	if strings.TrimSpace(cfg.Host) == "" {
		return nil, errors.New("host must be set")
	}
	if cfg.Username == "" {
		return nil, errors.New("username must be set")
	}
	if cfg.Password == "" {
		return nil, errors.New("password must be set")
	}
	base, err := baseURL(cfg.Host, cfg.Port)
	if err != nil {
		return nil, err
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.Insecure {
		tlsCfg.InsecureSkipVerify = true // explicit opt-in for self-signed router certificates
	}
	if len(cfg.CACertPEM) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(cfg.CACertPEM) {
			return nil, errors.New("ca_certificate does not contain a valid PEM certificate")
		}
		tlsCfg.RootCAs = pool
	}
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{
		Timeout: cfg.Timeout,
		Jar:     jar,
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			TLSClientConfig:     tlsCfg,
			DialContext:         (&net.Dialer{Timeout: cfg.Timeout, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout: cfg.Timeout,
			MaxIdleConns:        4,
			IdleConnTimeout:     90 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &Transport{cfg: cfg, base: base, hc: hc}, nil
}

func baseURL(host string, port int) (*url.URL, error) {
	h := strings.TrimSpace(host)
	if !strings.Contains(h, "://") {
		h = "https://" + h
	}
	u, err := url.Parse(h)
	if err != nil {
		return nil, fmt.Errorf("invalid host %q: %w", host, err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("invalid host %q: scheme must be https", host)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("invalid host %q", host)
	}
	if port > 0 {
		u.Host = net.JoinHostPort(u.Hostname(), strconv.Itoa(port))
	}
	u.Path, u.RawQuery, u.Fragment = "", "", ""
	return u, nil
}

// Name implements client.Transport.
func (t *Transport) Name() string { return "api" }

// BaseURL returns the URL requests are sent to.
func (t *Transport) BaseURL() string { return t.base.String() }

// Logins returns how many times the transport has logged in. It is used by
// tests to check session renewal.
func (t *Transport) Logins() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.logins
}

func (t *Transport) logf(format string, args ...any) {
	if t.cfg.Logf != nil {
		t.cfg.Logf(format, args...)
	}
}

// Login authenticates and stores the session cookies.
func (t *Transport) Login(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.loginLocked(ctx)
}

func (t *Transport) loginLocked(ctx context.Context) error {
	form := url.Values{"username": {t.cfg.Username}, "password": {t.cfg.Password}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.base.String()+"/", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	t.decorate(req)
	t.logf("logging in to %s as %s", t.base.Host, t.cfg.Username)
	resp, err := t.hc.Do(req)
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", t.base.Host, err)
	}
	defer drain(resp)
	switch {
	case resp.StatusCode == http.StatusOK:
		// The login page is served again when the credentials are wrong.
		return fmt.Errorf("%w: %s rejected the username or password", client.ErrAuth, t.base.Host)
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
	default:
		return fmt.Errorf("login to %s failed: HTTP %d", t.base.Host, resp.StatusCode)
	}
	if t.cookie(sessionCookie) == "" {
		return fmt.Errorf("login to %s did not return a session cookie", t.base.Host)
	}
	t.loggedIn = true
	t.logins++
	return nil
}

func (t *Transport) cookie(name string) string {
	for _, c := range t.hc.Jar.Cookies(t.base) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

func (t *Transport) decorate(req *http.Request) {
	if t.cfg.UserAgent != "" {
		req.Header.Set("User-Agent", t.cfg.UserAgent)
	}
	req.Header.Set("Accept", "application/json")
}

// errSessionExpired marks responses that mean "log in again".
var errSessionExpired = errors.New("session expired")

// do sends a request built by mk, logging in first if needed and once more
// if the session has expired. A 403 means the request was not executed, so
// retrying a write is safe.
func (t *Transport) do(ctx context.Context, mk func() (*http.Request, error)) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		t.mu.Lock()
		if !t.loggedIn {
			if err := t.loginLocked(ctx); err != nil {
				t.mu.Unlock()
				return nil, err
			}
		}
		t.mu.Unlock()

		body, err := t.send(mk)
		if errors.Is(err, errSessionExpired) && attempt == 0 {
			t.logf("session expired, logging in again")
			t.mu.Lock()
			t.loggedIn = false
			t.mu.Unlock()
			continue
		}
		if errors.Is(err, errSessionExpired) {
			return nil, fmt.Errorf("%w: %s still rejects the session after logging in again (does the user have admin rights?)", err, t.base.Host)
		}
		return body, err
	}
}

func (t *Transport) send(mk func() (*http.Request, error)) ([]byte, error) {
	req, err := mk()
	if err != nil {
		return nil, err
	}
	t.decorate(req)
	if req.Method != http.MethodGet {
		req.Header.Set(csrfHeader, t.cookie(csrfCookie))
	}
	resp, err := t.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request to %s failed: %w", t.base.Host, err)
	}
	defer drain(resp)
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("reading response from %s: %w", t.base.Host, err)
	}
	switch {
	case resp.StatusCode == http.StatusForbidden, resp.StatusCode == http.StatusUnauthorized:
		return nil, errSessionExpired
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		// Some firmware redirects to the login page instead of 403.
		return nil, errSessionExpired
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%s %s: HTTP %d: %s", req.Method, req.URL.Path, resp.StatusCode, snippet(body))
	}
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "text/html") {
		return nil, errSessionExpired
	}
	return body, nil
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

type getResponse struct {
	GET     map[string]any  `json:"GET"`
	Success json.RawMessage `json:"success"`
}

// Read implements client.Transport. It asks for the parent of path so a
// missing node can be told apart from a valueless one: partial.json answers
// null for both.
func (t *Transport) Read(ctx context.Context, path configtree.Path) (any, bool, error) {
	if len(path) == 0 {
		return nil, false, errors.New("cannot read the whole config tree")
	}
	parent := path.Parent()
	var structure any
	if len(parent) == 0 {
		structure = map[string]any{path[0]: nil}
	} else {
		structure = nest(parent, nil)
	}
	raw, err := json.Marshal(structure)
	if err != nil {
		return nil, false, err
	}
	q := url.Values{"struct": {string(raw)}}
	body, err := t.do(ctx, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, t.base.String()+"/api/edge/partial.json?"+q.Encode(), nil)
	})
	if err != nil {
		return nil, false, err
	}
	var resp getResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, false, fmt.Errorf("partial.json returned invalid JSON: %w: %s", err, snippet(body))
	}
	if !truthy(resp.Success, true) {
		return nil, false, fmt.Errorf("partial.json for %q failed: %s", path.String(), snippet(body))
	}
	tree := configtree.Normalize(resp.GET)
	if len(parent) == 0 {
		top, ok := configtree.Lookup(tree, path)
		// An unconfigured top level node is reported as null or "".
		if !ok || top == nil || top == "" {
			return nil, false, nil
		}
		return top, true, nil
	}
	p, ok := configtree.Lookup(tree, parent)
	if !ok {
		return nil, false, nil
	}
	m, isMap := p.(map[string]any)
	if !isMap {
		return nil, false, nil
	}
	v, ok := m[path.Last()]
	if !ok {
		return nil, false, nil
	}
	return v, true, nil
}

func nest(p configtree.Path, leaf any) any {
	out := leaf
	for i := len(p) - 1; i >= 0; i-- {
		out = map[string]any{p[i]: out}
	}
	return out
}

// stage is the per-step result in a batch.json response.
type stage struct {
	Success json.RawMessage `json:"success"`
	Failure json.RawMessage `json:"failure"`
	Error   json.RawMessage `json:"error"`
}

// Apply implements client.Transport.
func (t *Transport) Apply(ctx context.Context, b *configtree.Batch) error {
	payload, err := json.Marshal(b)
	if err != nil {
		return err
	}
	t.logf("batch.json:\n%s", b.String())
	body, err := t.do(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.base.String()+"/api/edge/batch.json", bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
	if err != nil {
		return err
	}
	return ParseBatchResponse(body)
}

// ParseBatchResponse turns a batch.json response into nil or a
// *client.CommitError.
func ParseBatchResponse(body []byte) error {
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("batch.json returned invalid JSON: %w: %s", err, snippet(body))
	}
	sawStage := false
	for _, name := range []string{"DELETE", "SET", "COMMIT", "SAVE"} {
		raw, ok := resp[name]
		if !ok {
			continue
		}
		sawStage = true
		var st stage
		if err := json.Unmarshal(raw, &st); err != nil {
			continue
		}
		failed := truthy(st.Failure, false)
		if !failed && len(st.Success) > 0 && !truthy(st.Success, true) {
			failed = true
		}
		if !failed {
			continue
		}
		errs := parseStageErrors(st.Error)
		if len(errs) == 0 {
			errs = []client.PathError{{Message: "unspecified " + strings.ToLower(name) + " error"}}
		}
		return &client.CommitError{Stage: name, Errors: errs}
	}
	if raw, ok := resp["success"]; ok && !truthy(raw, true) {
		return &client.CommitError{Stage: "BATCH", Errors: []client.PathError{{Message: snippet(body)}}}
	}
	if !sawStage {
		return fmt.Errorf("batch.json response has no result: %s", snippet(body))
	}
	return nil
}

// Errors come either as a map of path to message or as one string where
// entries are separated by U+FFFE and look like "[ path ]\nmessage" followed
// by U+FFFF and a status digit.
var entryRE = regexp.MustCompile(`(?s)\[\s(.*?)\s\]\n(.*?)\s*\x{FFFF}([01])`)

func parseStageErrors(raw json.RawMessage) []client.PathError {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		var out []client.PathError
		for _, part := range strings.Split(asString, "￾") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if m := entryRE.FindStringSubmatch(part); m != nil {
				if m[3] == "1" {
					continue
				}
				out = append(out, client.PathError{Path: m[1], Message: cleanMessage(m[2])})
				continue
			}
			out = append(out, client.PathError{Message: cleanMessage(part)})
		}
		return out
	}
	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err == nil {
		keys := make([]string, 0, len(asMap))
		for k := range asMap {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var out []client.PathError
		for _, k := range keys {
			msg := fmt.Sprint(asMap[k])
			if i := strings.IndexRune(msg, '￿'); i >= 0 {
				if strings.HasPrefix(msg[i+len("￿"):], "1") {
					continue
				}
				msg = msg[:i]
			}
			out = append(out, client.PathError{Path: k, Message: cleanMessage(msg)})
		}
		return out
	}
	return []client.PathError{{Message: cleanMessage(string(raw))}}
}

func cleanMessage(s string) string {
	s = strings.ReplaceAll(s, "￿", "")
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, " ")
}

// truthy reads the loosely typed status fields: "1", 1, true, "true".
func truthy(raw json.RawMessage, def bool) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return def
	}
	var x any
	if err := json.Unmarshal(raw, &x); err != nil {
		return def
	}
	switch v := x.(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes":
			return true
		case "0", "false", "no", "":
			return false
		}
	}
	return def
}

// SystemInfo implements client.Transport.
func (t *Transport) SystemInfo(ctx context.Context) (client.SystemInfo, error) {
	body, err := t.do(ctx, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, t.base.String()+"/api/edge/data.json?data=sys_info", nil)
	})
	if err != nil {
		return client.SystemInfo{}, err
	}
	var resp struct {
		Success json.RawMessage `json:"success"`
		Output  struct {
			SwVer string `json:"sw_ver"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return client.SystemInfo{}, fmt.Errorf("data.json returned invalid JSON: %w", err)
	}
	if !truthy(resp.Success, true) || resp.Output.SwVer == "" {
		return client.SystemInfo{}, fmt.Errorf("data.json sys_info failed: %s", snippet(body))
	}
	return client.ParseSoftwareVersion(resp.Output.SwVer), nil
}

var _ client.Transport = (*Transport)(nil)
