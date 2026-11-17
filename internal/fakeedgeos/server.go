// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

// Package fakeedgeos is an in-memory imitation of the EdgeOS web UI API. It
// implements login, get.json, partial.json, batch.json, data.json and
// heartbeat.json with the same value encoding as a real EdgeRouter, plus
// set-time and commit-time validation and hooks to inject failures.
//
// It exists so the provider can be tested without a router. It is not a
// complete EdgeOS model: validation covers the nodes the provider manages and
// the errors worth testing.
package fakeedgeos

import (
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

// Options configures a fake server.
type Options struct {
	Username string
	Password string
	// SoftwareVersion is returned by data.json?data=sys_info.
	SoftwareVersion string
	// Tree replaces the default initial config. It must be a normalized
	// tree (see configtree.Normalize).
	Tree map[string]any
	// CommitDelay makes every commit take this long, which helps tests
	// detect overlapping commits.
	CommitDelay time.Duration
}

// Default credentials used when Options leaves them empty.
const (
	DefaultUsername        = "admin"
	DefaultPassword        = "fake-password!"
	DefaultSoftwareVersion = "EdgeRouter.ER-e50.v3.0.1.0000001.250101.0000"
)

// topLevel lists the nodes get.json returns. port-forward and a few others
// are only reachable through partial.json, as on a real device.
var topLevel = []string{
	"firewall", "interfaces", "layer2", "service", "system", "vpn",
	"protocols", "traffic-control", "onu-list", "onu-profiles", "onu-policies",
}

// Server is a fake EdgeRouter.
type Server struct {
	opts Options

	mu       sync.Mutex
	tree     map[string]any
	sessions map[string]string // session id -> csrf token

	failNext    []string
	committing  bool
	logins      int
	batches     int
	overlaps    int
	commits     int
	lastBatch   map[string]any
	failedLogin int

	httpServer *httptest.Server
}

// New returns a fake server with the default or given initial config. Call
// Start or StartOn to serve it.
func New(opts Options) *Server {
	if opts.Username == "" {
		opts.Username = DefaultUsername
	}
	if opts.Password == "" {
		opts.Password = DefaultPassword
	}
	if opts.SoftwareVersion == "" {
		opts.SoftwareVersion = DefaultSoftwareVersion
	}
	tree := opts.Tree
	if tree == nil {
		tree = DefaultTree()
	} else {
		tree, _ = configtree.Normalize(tree).(map[string]any)
	}
	return &Server{opts: opts, tree: tree, sessions: map[string]string{}}
}

// DefaultTree is a factory-like config with five ethernet ports, built only
// from documentation addresses.
func DefaultTree() map[string]any {
	eth := map[string]any{}
	for i := 0; i < 5; i++ {
		port := map[string]any{"duplex": "auto", "speed": "auto"}
		if i == 1 {
			port["address"] = []string{"192.168.1.1/24"}
		}
		eth[fmt.Sprintf("eth%d", i)] = port
	}
	return map[string]any{
		// The global firewall options of a factory config.boot.
		"firewall": map[string]any{
			"all-ping":               "enable",
			"broadcast-ping":         "disable",
			"ipv6-receive-redirects": "disable",
			"ipv6-src-route":         "disable",
			"ip-src-route":           "disable",
			"log-martians":           "enable",
			"receive-redirects":      "disable",
			"send-redirects":         "enable",
			"source-validation":      "disable",
			"syn-cookies":            "enable",
		},
		"interfaces": map[string]any{
			"ethernet": eth,
			"loopback": map[string]any{"lo": nil},
		},
		"service": map[string]any{
			"gui": map[string]any{"http-port": "80", "https-port": "443", "older-ciphers": "enable"},
			"ssh": map[string]any{"port": "22", "protocol-version": "v2"},
		},
		"system": map[string]any{
			"host-name": "ubnt",
			"login": map[string]any{"user": map[string]any{DefaultUsername: map[string]any{
				"authentication": map[string]any{"encrypted-password": "$6$fake$notarealhash", "plaintext-password": ""},
				"level":          "admin",
			}}},
			"ntp": map[string]any{"server": map[string]any{
				"0.ubnt.pool.ntp.org": nil,
				"1.ubnt.pool.ntp.org": nil,
			}},
			"syslog": map[string]any{"global": map[string]any{"facility": map[string]any{
				"all":       map[string]any{"level": "notice"},
				"protocols": map[string]any{"level": "debug"},
			}}},
			"time-zone": "UTC",
		},
	}
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/api/edge/get.json", s.authed(s.handleGet))
	mux.HandleFunc("/api/edge/partial.json", s.authed(s.handlePartial))
	mux.HandleFunc("/api/edge/batch.json", s.authed(s.handleBatch))
	mux.HandleFunc("/api/edge/data.json", s.authed(s.handleData))
	mux.HandleFunc("/api/edge/heartbeat.json", s.authed(s.handleHeartbeat))
	return mux
}

// Start serves on a random local port with a self-signed certificate and
// returns the base URL.
func (s *Server) Start() string {
	s.httpServer = httptest.NewTLSServer(s.Handler())
	return s.httpServer.URL
}

// StartOn serves on addr (for example "127.0.0.1:8443") with a self-signed
// certificate.
func (s *Server) StartOn(addr string) (string, error) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	srv := httptest.NewUnstartedServer(s.Handler())
	_ = srv.Listener.Close()
	srv.Listener = l
	srv.StartTLS()
	s.httpServer = srv
	return srv.URL, nil
}

// Close stops the server.
func (s *Server) Close() {
	if s.httpServer != nil {
		s.httpServer.Close()
	}
}

// Certificate returns the self-signed certificate once started.
func (s *Server) Certificate() *x509.Certificate {
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Certificate()
}

// URL returns the base URL once started.
func (s *Server) URL() string {
	if s.httpServer == nil {
		return ""
	}
	return s.httpServer.URL
}

// HostPort returns host and port of the running server.
func (s *Server) HostPort() (string, string) {
	u, _ := url.Parse(s.URL())
	return u.Hostname(), u.Port()
}

func randomToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

const loginPage = `<!DOCTYPE html><html><head><title>EdgeOS</title></head><body><form method="post" action="/"><input name="username"><input name="password" type="password"></form></body></html>`

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, loginPage)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	ok := r.PostForm.Get("username") == s.opts.Username && r.PostForm.Get("password") == s.opts.Password
	if !ok {
		s.failedLogin++
		s.mu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, loginPage)
		return
	}
	id, csrf := randomToken(), randomToken()
	s.sessions[id] = csrf
	s.logins++
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "PHPSESSID", Value: id, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	// The CSRF cookie is read by the GUI's JavaScript, so it is not HttpOnly.
	http.SetCookie(w, &http.Cookie{Name: "X-CSRF-TOKEN", Value: csrf, Path: "/", Secure: true, SameSite: http.SameSiteStrictMode}) //nolint:gosec // matches EdgeOS
	w.Header().Set("Location", "/")
	w.WriteHeader(http.StatusSeeOther)
}

func (s *Server) authed(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("PHPSESSID")
		s.mu.Lock()
		csrf, ok := "", false
		if err == nil {
			csrf, ok = s.sessions[c.Value]
		}
		s.mu.Unlock()
		if !ok {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "<html><title>Error: 403 Forbidden</title></html>")
			return
		}
		if r.Method == http.MethodPost && r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
			if r.Header.Get("X-CSRF-TOKEN") != csrf {
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, "Validation of CSRF token failed.")
				return
			}
		}
		next(w, r, c.Value)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleGet(w http.ResponseWriter, _ *http.Request, sid string) {
	s.mu.Lock()
	out := map[string]any{}
	for _, k := range topLevel {
		if v, ok := s.tree[k]; ok && !isEmpty(v) {
			out[k] = configtree.DeepCopy(v)
		} else {
			out[k] = ""
		}
	}
	s.mu.Unlock()
	writeJSON(w, map[string]any{"SESSION_ID": sid, "GET": out, "success": true})
}

func isEmpty(v any) bool {
	m, ok := v.(map[string]any)
	return v == nil || (ok && len(m) == 0)
}

func (s *Server) handlePartial(w http.ResponseWriter, r *http.Request, sid string) {
	var structure map[string]any
	if err := json.Unmarshal([]byte(r.URL.Query().Get("struct")), &structure); err != nil {
		writeJSON(w, map[string]any{"success": false, "error": "invalid struct"})
		return
	}
	s.mu.Lock()
	out := project(s.tree, structure)
	s.mu.Unlock()
	writeJSON(w, map[string]any{"SESSION_ID": sid, "GET": out, "success": true})
}

// project returns the parts of tree selected by structure. A nil in the
// structure selects the whole subtree; a missing node comes back as nil,
// which is what partial.json does.
func project(tree any, structure map[string]any) map[string]any {
	out := map[string]any{}
	m, _ := tree.(map[string]any)
	for k, sel := range structure {
		v, ok := m[k]
		if !ok {
			out[k] = nil
			continue
		}
		if sub, isMap := sel.(map[string]any); isMap && len(sub) > 0 {
			out[k] = project(v, sub)
			continue
		}
		out[k] = configtree.DeepCopy(v)
	}
	return out
}

func (s *Server) handleData(w http.ResponseWriter, r *http.Request, _ string) {
	switch r.URL.Query().Get("data") {
	case "sys_info":
		writeJSON(w, map[string]any{"success": "1", "output": map[string]any{
			"sw_ver": s.opts.SoftwareVersion,
			"fw-latest": map[string]any{
				"version": "v3.0.1",
				"state":   "up-to-date",
			},
		}})
	default:
		writeJSON(w, map[string]any{"success": "0", "error": "unknown data type"})
	}
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, _ *http.Request, _ string) {
	writeJSON(w, map[string]any{"SESSION": true, "PING": true})
}

type batchRequest struct {
	SET    map[string]any `json:"SET"`
	DELETE map[string]any `json:"DELETE"`
}

func (s *Server) handleBatch(w http.ResponseWriter, r *http.Request, sid string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req batchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, map[string]any{"success": false, "error": "invalid JSON"})
		return
	}

	s.mu.Lock()
	s.batches++
	s.lastBatch = map[string]any{"SET": req.SET, "DELETE": req.DELETE}
	if s.committing {
		s.overlaps++
		s.mu.Unlock()
		writeJSON(w, map[string]any{"SESSION_ID": sid, "success": true,
			"COMMIT": map[string]any{"failure": "1", "success": "0",
				"error": commitErrorString([]commitIssue{{Path: "", Msg: "Configuration system temporarily locked due to another commit in progress"}})}})
		return
	}
	s.committing = true
	candidate, _ := configtree.DeepCopy(s.tree).(map[string]any)
	s.mu.Unlock()

	resp := s.apply(candidate, req)
	if s.opts.CommitDelay > 0 {
		time.Sleep(s.opts.CommitDelay)
	}

	s.mu.Lock()
	s.committing = false
	if ok, _ := resp["_ok"].(bool); ok {
		s.tree = candidate
		s.commits++
	}
	delete(resp, "_ok")
	s.mu.Unlock()
	resp["SESSION_ID"] = sid
	resp["success"] = true
	writeJSON(w, resp)
}

// apply runs DELETE, SET and the commit checks against candidate.
func (s *Server) apply(candidate map[string]any, req batchRequest) map[string]any {
	resp := map[string]any{"_ok": false}
	if req.DELETE != nil {
		var missing []string
		for _, p := range leafPaths(req.DELETE) {
			if !configtree.DeleteIn(candidate, p) {
				missing = append(missing, p.String())
			}
		}
		if len(missing) > 0 {
			errs := map[string]any{}
			for _, m := range missing {
				errs[m] = "Nothing to delete (the specified node does not exist)￿0"
			}
			resp["DELETE"] = map[string]any{"failure": "1", "success": "0", "error": errs}
			return resp
		}
		resp["DELETE"] = map[string]any{"failure": "0", "success": "1"}
	}
	if req.SET != nil {
		errs := map[string]any{}
		for _, op := range setOps(req.SET) {
			if msg := validateSet(op.Path, op.Value); msg != "" {
				errs[op.Path.String()] = msg + "￿0"
				continue
			}
			setValue(candidate, op.Path, op.Value)
		}
		if len(errs) > 0 {
			resp["SET"] = map[string]any{"failure": "1", "success": "0", "error": errs}
			return resp
		}
		resp["SET"] = map[string]any{"failure": "0", "success": "1"}
	}

	s.mu.Lock()
	var forced []string
	if len(s.failNext) > 0 {
		forced = append(forced, s.failNext[0])
		s.failNext = s.failNext[1:]
	}
	s.mu.Unlock()

	issues := validateCommit(candidate)
	for _, f := range forced {
		issues = append(issues, commitIssue{Msg: f})
	}
	if len(issues) > 0 {
		resp["COMMIT"] = map[string]any{"failure": "1", "success": "0", "error": commitErrorString(issues)}
		return resp
	}
	postCommit(candidate)
	resp["COMMIT"] = map[string]any{"failure": "0", "success": "1"}
	resp["SAVE"] = map[string]any{"success": "1"}
	resp["_ok"] = true
	return resp
}

type commitIssue struct {
	Path string
	Msg  string
}

// commitErrorString renders issues the way the EdgeOS daemon does: entries
// separated by U+FFFE, each "[ path ]\nmessage" followed by U+FFFF0.
func commitErrorString(issues []commitIssue) string {
	parts := make([]string, 0, len(issues))
	for _, is := range issues {
		if is.Path == "" {
			parts = append(parts, is.Msg+"\n\nCommit failed￿0")
			continue
		}
		parts = append(parts, "[ "+is.Path+" ]\n"+is.Msg+"\n\nCommit failed￿0")
	}
	return strings.Join(parts, "￾")
}

// leafPaths lists the paths marked with null in a DELETE document.
func leafPaths(doc map[string]any) []configtree.Path {
	var out []configtree.Path
	var walk func(x any, at configtree.Path)
	walk = func(x any, at configtree.Path) {
		m, ok := x.(map[string]any)
		if !ok || len(m) == 0 {
			if len(at) > 0 {
				out = append(out, at)
			}
			return
		}
		for k, v := range m {
			walk(v, at.Child(k))
		}
	}
	walk(doc, configtree.Path{})
	sortPaths(out)
	return out
}

// setOps lists the leaves of a SET document.
func setOps(doc map[string]any) []configtree.Op {
	l := configtree.Flatten(configtree.Normalize(doc))
	ops := make([]configtree.Op, 0, l.Len())
	for _, p := range l.Paths() {
		v, _ := l.Get(p)
		ops = append(ops, configtree.Op{Path: p, Value: v})
	}
	return ops
}

func setValue(tree map[string]any, p configtree.Path, v configtree.Value) {
	if v.Kind == configtree.KindSingle && isMultiNode(p) {
		v = configtree.Multi(v.Values...)
	}
	configtree.SetIn(tree, p, v)
}

// Test hooks.

// FailNextCommit makes the next commit fail with msg.
func (s *Server) FailNextCommit(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNext = append(s.failNext, msg)
}

// ExpireSessions logs every client out, like a session timeout or reboot.
func (s *Server) ExpireSessions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = map[string]string{}
}

// Set changes the config out of band, without validation.
func (s *Server) Set(p configtree.Path, v configtree.Value) {
	s.mu.Lock()
	defer s.mu.Unlock()
	setValue(s.tree, p, v)
}

// Delete removes a node out of band.
func (s *Server) Delete(p configtree.Path) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return configtree.DeleteIn(s.tree, p)
}

// Get returns a copy of the node at path.
func (s *Server) Get(p configtree.Path) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := configtree.Lookup(s.tree, p)
	return configtree.DeepCopy(v), ok
}

// Tree returns a copy of the whole config.
func (s *Server) Tree() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, _ := configtree.DeepCopy(s.tree).(map[string]any)
	return t
}

// Stats reports request counters.
type Stats struct {
	Logins       int
	FailedLogins int
	Batches      int
	Commits      int
	Overlaps     int
}

// Stats returns the counters.
func (s *Server) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{Logins: s.logins, FailedLogins: s.failedLogin, Batches: s.batches, Commits: s.commits, Overlaps: s.overlaps}
}

// LastBatch returns the body of the most recent batch.json request.
func (s *Server) LastBatch() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastBatch
}
