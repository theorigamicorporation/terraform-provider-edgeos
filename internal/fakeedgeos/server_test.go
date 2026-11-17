// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package fakeedgeos

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
)

func p(s string) configtree.Path {
	path, err := configtree.ParsePath(s)
	if err != nil {
		panic(err)
	}
	return path
}

// session logs in with a raw HTTP client, the way a browser would.
type session struct {
	t    *testing.T
	base string
	hc   *http.Client
	csrf string
}

func login(t *testing.T, s *Server) *session {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{
		Jar:       jar,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := hc.PostForm(s.URL()+"/", url.Values{"username": {DefaultUsername}, "password": {DefaultPassword}})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login status %d", resp.StatusCode)
	}
	u, _ := url.Parse(s.URL())
	ss := &session{t: t, base: s.URL(), hc: hc}
	for _, c := range jar.Cookies(u) {
		if c.Name == "X-CSRF-TOKEN" {
			ss.csrf = c.Value
		}
	}
	return ss
}

func (s *session) do(method, path string, body any, csrf bool) (int, map[string]any) {
	s.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, s.base+path, rd)
	if csrf {
		req.Header.Set("X-CSRF-TOKEN", s.csrf)
	}
	resp, err := s.hc.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func newServer(t *testing.T, opts Options) *Server {
	t.Helper()
	s := New(opts)
	s.Start()
	t.Cleanup(s.Close)
	return s
}

func TestLoginAndAuth(t *testing.T) {
	t.Parallel()
	s := newServer(t, Options{})
	ss := login(t, s)
	if code, _ := ss.do(http.MethodGet, "/api/edge/get.json", nil, false); code != http.StatusOK {
		t.Fatalf("get.json status %d", code)
	}
	if code, _ := ss.do(http.MethodPost, "/api/edge/batch.json", map[string]any{"SET": map[string]any{"system": map[string]any{"host-name": "x"}}}, false); code != http.StatusForbidden {
		t.Fatalf("batch without CSRF token: %d", code)
	}
	s.ExpireSessions()
	if code, _ := ss.do(http.MethodGet, "/api/edge/get.json", nil, false); code != http.StatusForbidden {
		t.Fatalf("expired session: %d", code)
	}
	if st := s.Stats(); st.Logins != 1 {
		t.Fatalf("stats %+v", st)
	}
	if New(Options{}).URL() != "" || New(Options{}).Certificate() != nil {
		t.Fatal("unstarted server")
	}
}

func TestBadLoginCounts(t *testing.T) {
	t.Parallel()
	s := newServer(t, Options{})
	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	resp, err := hc.PostForm(s.URL()+"/", url.Values{"username": {"admin"}, "password": {"nope"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "<form") {
		t.Fatalf("bad login: %d %s", resp.StatusCode, body)
	}
	if s.Stats().FailedLogins != 1 {
		t.Fatal("failed login not counted")
	}
	resp, err = hc.Get(s.URL() + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	resp, err = hc.Get(s.URL() + "/nope")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown path: %d", resp.StatusCode)
	}
}

func TestGetAndPartial(t *testing.T) {
	t.Parallel()
	s := newServer(t, Options{})
	s.Set(p("port-forward wan-interface"), configtree.Single("eth0"))
	ss := login(t, s)
	_, out := ss.do(http.MethodGet, "/api/edge/get.json", nil, false)
	get, _ := out["GET"].(map[string]any)
	if get["vpn"] != "" || get["system"] == nil {
		t.Fatalf("get.json: %#v", get)
	}
	if _, ok := get["port-forward"]; ok {
		t.Fatal("get.json must not return port-forward")
	}
	q := url.Values{"struct": {`{"port-forward": null, "system": {"host-name": null, "nope": null}}`}}
	_, out = ss.do(http.MethodGet, "/api/edge/partial.json?"+q.Encode(), nil, false)
	want := map[string]any{
		"port-forward": map[string]any{"wan-interface": "eth0"},
		"system":       map[string]any{"host-name": "ubnt", "nope": nil},
	}
	if !reflect.DeepEqual(out["GET"], want) {
		t.Fatalf("partial: %#v", out["GET"])
	}
	_, out = ss.do(http.MethodGet, "/api/edge/partial.json?struct=nope", nil, false)
	if out["success"] != false {
		t.Fatalf("bad struct: %#v", out)
	}
	_, out = ss.do(http.MethodGet, "/api/edge/data.json?data=sys_info", nil, false)
	if out["success"] != "1" {
		t.Fatalf("sys_info: %#v", out)
	}
	_, out = ss.do(http.MethodGet, "/api/edge/data.json?data=other", nil, false)
	if out["success"] != "0" {
		t.Fatalf("unknown data: %#v", out)
	}
	_, out = ss.do(http.MethodGet, "/api/edge/heartbeat.json", nil, false)
	if out["PING"] != true {
		t.Fatalf("heartbeat: %#v", out)
	}
}

func TestBatch(t *testing.T) {
	t.Parallel()
	s := newServer(t, Options{})
	ss := login(t, s)
	batch := func(body any) map[string]any {
		_, out := ss.do(http.MethodPost, "/api/edge/batch.json", body, true)
		return out
	}

	// A single string on a multi-valued node is stored as a list.
	out := batch(map[string]any{"SET": map[string]any{"system": map[string]any{"name-server": "192.0.2.53"}}})
	if commit, _ := out["COMMIT"].(map[string]any); commit["success"] != "1" {
		t.Fatalf("commit: %#v", out)
	}
	if v, _ := s.Get(p("system name-server")); !reflect.DeepEqual(v, []string{"192.0.2.53"}) {
		t.Fatalf("name-server = %#v", v)
	}

	// Delete then set in one batch replaces the value.
	batch(map[string]any{
		"DELETE": map[string]any{"system": map[string]any{"name-server": nil}},
		"SET":    map[string]any{"system": map[string]any{"name-server": []string{"198.51.100.53"}}},
	})
	if v, _ := s.Get(p("system name-server")); !reflect.DeepEqual(v, []string{"198.51.100.53"}) {
		t.Fatalf("name-server = %#v", v)
	}

	// Set-time validation error.
	out = batch(map[string]any{"SET": map[string]any{"system": map[string]any{"host-name": `bad"quote`}}})
	if set, _ := out["SET"].(map[string]any); set["failure"] != "1" {
		t.Fatalf("set failure expected: %#v", out)
	}

	// Commit-time error, the tree stays unchanged.
	before := s.Tree()
	out = batch(map[string]any{"SET": map[string]any{"firewall": map[string]any{"name": map[string]any{"X": map[string]any{"rule": map[string]any{"10": map[string]any{"description": "no action"}}}}}}})
	commit, _ := out["COMMIT"].(map[string]any)
	if commit["failure"] != "1" || !strings.Contains(commit["error"].(string), "[ firewall name X rule 10 ]") {
		t.Fatalf("commit failure expected: %#v", out)
	}
	if !reflect.DeepEqual(before, s.Tree()) {
		t.Fatal("failed commit changed the tree")
	}

	// Bad JSON and wrong method.
	req, _ := http.NewRequest(http.MethodPost, s.URL()+"/api/edge/batch.json", strings.NewReader("{"))
	req.Header.Set("X-CSRF-TOKEN", ss.csrf)
	resp, err := ss.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if code, _ := ss.do(http.MethodGet, "/api/edge/batch.json", nil, true); code != http.StatusMethodNotAllowed {
		t.Fatalf("GET batch: %d", code)
	}
	if s.LastBatch() == nil {
		t.Fatal("LastBatch")
	}
}

func TestPasswordsAreHashed(t *testing.T) {
	t.Parallel()
	s := newServer(t, Options{})
	ss := login(t, s)
	user := func(pw string) map[string]any {
		_, out := ss.do(http.MethodPost, "/api/edge/batch.json", map[string]any{"SET": map[string]any{"system": map[string]any{"login": map[string]any{"user": map[string]any{
			"ops": map[string]any{"authentication": map[string]any{"plaintext-password": pw}, "level": "operator"},
		}}}}}, true)
		return out
	}
	out := user("onlyletters1")
	if set, _ := out["SET"].(map[string]any); set["failure"] != "1" {
		t.Fatalf("alphanumeric password accepted: %#v", out)
	}
	user("with-symbol!")
	auth, _ := s.Get(p("system login user ops authentication"))
	m, _ := auth.(map[string]any)
	if m["plaintext-password"] != "" || !strings.HasPrefix(m["encrypted-password"].(string), "$6$") {
		t.Fatalf("auth after commit: %#v", m)
	}
}

func TestCommitChecks(t *testing.T) {
	t.Parallel()
	tree := map[string]any{
		"interfaces": map[string]any{
			"ethernet":  map[string]any{"eth0": map[string]any{"firewall": map[string]any{"in": map[string]any{"name": "MISSING"}}}},
			"wireguard": map[string]any{"wg0": map[string]any{"peer": map[string]any{"k": map[string]any{"description": "x"}}}},
		},
		"service": map[string]any{
			"nat": map[string]any{"rule": map[string]any{
				"10":   map[string]any{"type": "destination"},
				"5000": map[string]any{"type": "masquerade"},
				"5001": map[string]any{"description": "no type"},
			}},
			"dhcp-server": map[string]any{"shared-network-name": map[string]any{"LAN": map[string]any{"subnet": map[string]any{
				"192.168.1.0/24": map[string]any{"static-mapping": map[string]any{"p": map[string]any{"ip-address": "192.168.1.5"}}},
			}}}},
		},
		"port-forward": map[string]any{"rule": map[string]any{"1": map[string]any{"original-port": "80"}}},
	}
	issues := validateCommit(tree)
	var msgs []string
	for _, is := range issues {
		msgs = append(msgs, is.Path+": "+is.Msg)
	}
	joined := strings.Join(msgs, "\n")
	for _, want := range []string{
		"Firewall name [MISSING] does not exist",
		"service nat rule 10: inbound-interface not specified",
		"service nat rule 5000: outbound-interface not specified",
		"service nat rule 5001: Rule type must be set",
		"port-forward: wan-interface must be set",
		"port-forward: lan-interface must be set",
		"interfaces wireguard wg0: private-key must be set",
		"allowed-ips must be set",
		"static-mapping p: mac-address must be set",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing issue %q in:\n%s", want, joined)
		}
	}
}

func TestValidateSet(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path  string
		value configtree.Value
		want  string
	}{
		{"system host-name", configtree.Single("ok"), ""},
		{"firewall name A rule 10 action", configtree.Single("accept"), ""},
		{"firewall name A rule 10 action", configtree.Single("boom"), "not one of"},
		{"firewall name A rule 0 action", configtree.Single("accept"), "between 1 and 9999"},
		{"firewall name " + strings.Repeat("x", 30) + " default-action", configtree.Single("drop"), "less than 28"},
		{"interfaces ethernet eth0 mtu", configtree.Single("9001"), "MTU"},
		{"interfaces ethernet eth0 mtu", configtree.Single("1500"), ""},
		{"interfaces ethernet lan0 mtu", configtree.Single("1500"), "not a valid ethernet"},
		{"interfaces ethernet eth0 vif 5000 mtu", configtree.Single("1500"), "VLAN ID"},
		{"interfaces wireguard tun0 mtu", configtree.Single("1420"), "wg must be"},
		{"interfaces ethernet eth0 mac", configtree.Single("zz"), "MAC"},
		{"service nat rule 0 type", configtree.Single("source"), "between 1 and 9999"},
		{"protocols static route nope next-hop 192.0.2.1 distance", configtree.Single("1"), "IPv4 prefix"},
		{"a b", configtree.Single(`x"y`), "double quote"},
		{`a "b`, configtree.Flag(), "double quote"},
	}
	for _, tc := range cases {
		path := strings.Fields(tc.path)
		got := validateSet(configtree.NewPath(path...), tc.value)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("validateSet(%s, %v) = %q, want %q", tc.path, tc.value, got, tc.want)
		}
	}
}

func TestOutOfBandHelpers(t *testing.T) {
	t.Parallel()
	s := New(Options{Tree: map[string]any{"system": map[string]any{"host-name": "seeded"}}})
	if v, _ := s.Get(p("system host-name")); v != "seeded" {
		t.Fatalf("seed: %v", v)
	}
	s.Set(p("system domain-name"), configtree.Single("example.net"))
	if !s.Delete(p("system domain-name")) || s.Delete(p("system domain-name")) {
		t.Fatal("Delete")
	}
	if len(DefaultTree()) == 0 {
		t.Fatal("DefaultTree")
	}
}
