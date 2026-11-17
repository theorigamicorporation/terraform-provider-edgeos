// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package webapi_test

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/client"
	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/client/webapi"
	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/configtree"
	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/fakeedgeos"
)

func newFake(t *testing.T, opts fakeedgeos.Options) (*fakeedgeos.Server, *webapi.Transport) {
	t.Helper()
	srv := fakeedgeos.New(opts)
	srv.Start()
	t.Cleanup(srv.Close)
	host, port := srv.HostPort()
	tr, err := webapi.New(webapi.Config{
		Host:     host,
		Port:     atoi(t, port),
		Username: fakeedgeos.DefaultUsername,
		Password: fakeedgeos.DefaultPassword,
		Insecure: true,
		Timeout:  5 * time.Second,
		Logf:     t.Logf,
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv, tr
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		t.Fatal(err)
	}
	return n
}

func p(s string) configtree.Path {
	path, err := configtree.ParsePath(s)
	if err != nil {
		panic(err)
	}
	return path
}

func TestNewValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  webapi.Config
		want string
	}{
		{"no host", webapi.Config{Username: "u", Password: "p"}, "host must be set"},
		{"no user", webapi.Config{Host: "192.0.2.1", Password: "p"}, "username must be set"},
		{"no password", webapi.Config{Host: "192.0.2.1", Username: "u"}, "password must be set"},
		{"bad scheme", webapi.Config{Host: "ftp://192.0.2.1", Username: "u", Password: "p"}, "scheme must be https"},
		{"bad url", webapi.Config{Host: "https://", Username: "u", Password: "p"}, "invalid host"},
		{"bad url parse", webapi.Config{Host: "https://[::1", Username: "u", Password: "p"}, "invalid host"},
		{"bad ca", webapi.Config{Host: "192.0.2.1", Username: "u", Password: "p", CACertPEM: []byte("nope")}, "valid PEM"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := webapi.New(tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestBaseURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		host string
		port int
		want string
	}{
		{"192.0.2.1", 0, "https://192.0.2.1"},
		{"192.0.2.1", 8443, "https://192.0.2.1:8443"},
		{"router.example.com:4443", 0, "https://router.example.com:4443"},
		{"https://router.example.com/some/path?x=1", 0, "https://router.example.com"},
		{"http://127.0.0.1:8080", 0, "http://127.0.0.1:8080"},
		{"[2001:db8::1]", 443, "https://[2001:db8::1]:443"},
	}
	for _, tc := range cases {
		tr, err := webapi.New(webapi.Config{Host: tc.host, Port: tc.port, Username: "u", Password: "p"})
		if err != nil {
			t.Fatalf("%s: %v", tc.host, err)
		}
		if got := tr.BaseURL(); got != tc.want {
			t.Errorf("BaseURL(%q, %d) = %q, want %q", tc.host, tc.port, got, tc.want)
		}
		if tr.Name() != "api" {
			t.Fatal("Name")
		}
	}
}

func TestLogin(t *testing.T) {
	t.Parallel()
	srv, tr := newFake(t, fakeedgeos.Options{})
	if err := tr.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	if srv.Stats().Logins != 1 || tr.Logins() != 1 {
		t.Fatalf("stats %+v", srv.Stats())
	}
}

func TestLoginBadPassword(t *testing.T) {
	t.Parallel()
	srv := fakeedgeos.New(fakeedgeos.Options{})
	srv.Start()
	t.Cleanup(srv.Close)
	host, port := srv.HostPort()
	tr, err := webapi.New(webapi.Config{Host: host, Port: atoi(t, port), Username: "admin", Password: "wrong", Insecure: true})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = tr.Read(context.Background(), p("system host-name"))
	if !errors.Is(err, client.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	if srv.Stats().FailedLogins != 1 {
		t.Fatalf("stats %+v", srv.Stats())
	}
}

func TestTLSVerification(t *testing.T) {
	t.Parallel()
	srv := fakeedgeos.New(fakeedgeos.Options{})
	srv.Start()
	t.Cleanup(srv.Close)
	host, port := srv.HostPort()
	tr, err := webapi.New(webapi.Config{Host: host, Port: atoi(t, port), Username: "admin", Password: "x"})
	if err != nil {
		t.Fatal(err)
	}
	err = tr.Login(context.Background())
	if err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("expected a certificate error, got %v", err)
	}
}

func TestRead(t *testing.T) {
	t.Parallel()
	srv, tr := newFake(t, fakeedgeos.Options{})
	srv.Set(p("firewall name WAN_IN default-action"), configtree.Single("drop"))
	srv.Set(p("firewall name WAN_IN rule 10 disable"), configtree.Flag())
	ctx := context.Background()
	cases := []struct {
		path   string
		want   any
		exists bool
	}{
		{"system host-name", "ubnt", true},
		{"interfaces ethernet eth1 address", []string{"192.168.1.1/24"}, true},
		{"firewall name WAN_IN rule 10 disable", nil, true},
		{"system ntp server 0.ubnt.pool.ntp.org", nil, true},
		{"firewall name WAN_IN rule 20", nil, false},
		{"firewall name NOPE rule 10", nil, false},
		{"firewall name NOPE", nil, false},
		{"system host-name nested", nil, false},
		{"port-forward", nil, false},
		{"vpn", nil, false},
		{"firewall name WAN_IN default-action", "drop", true},
	}
	for _, tc := range cases {
		got, ok, err := tr.Read(ctx, p(tc.path))
		if err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
		if ok != tc.exists || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Read(%s) = %#v, %v; want %#v, %v", tc.path, got, ok, tc.want, tc.exists)
		}
	}
	// Top level node with content.
	got, ok, err := tr.Read(ctx, p("service"))
	if err != nil || !ok {
		t.Fatalf("service: %v %v", ok, err)
	}
	if _, isMap := got.(map[string]any); !isMap {
		t.Fatalf("service = %#v", got)
	}
	if _, _, err := tr.Read(ctx, configtree.Path{}); err == nil {
		t.Fatal("reading root should fail")
	}
}

func TestApply(t *testing.T) {
	t.Parallel()
	srv, tr := newFake(t, fakeedgeos.Options{})
	ctx := context.Background()
	b := configtree.NewBatch()
	b.Set(p("firewall name LAN_IN default-action"), configtree.Single("accept"))
	b.Set(p("firewall name LAN_IN rule 10 action"), configtree.Single("drop"))
	b.Set(p("interfaces ethernet eth2 address"), configtree.Multi("192.0.2.1/24", "198.51.100.1/24"))
	if err := tr.Apply(ctx, b); err != nil {
		t.Fatal(err)
	}
	if v, _ := srv.Get(p("interfaces ethernet eth2 address")); !reflect.DeepEqual(v, []string{"192.0.2.1/24", "198.51.100.1/24"}) {
		t.Fatalf("address = %#v", v)
	}
	// Replace the address list.
	b = configtree.Diff(p("interfaces ethernet eth2"),
		leaves("address", configtree.Multi("192.0.2.1/24", "198.51.100.1/24")),
		leaves("address", configtree.Multi("203.0.113.1/24")))
	if err := tr.Apply(ctx, b); err != nil {
		t.Fatal(err)
	}
	if v, _ := srv.Get(p("interfaces ethernet eth2 address")); !reflect.DeepEqual(v, []string{"203.0.113.1/24"}) {
		t.Fatalf("address after replace = %#v", v)
	}
	// Deleting the whole rule set.
	b = configtree.NewBatch()
	b.Delete(p("firewall name LAN_IN"))
	if err := tr.Apply(ctx, b); err != nil {
		t.Fatal(err)
	}
	if _, ok := srv.Get(p("firewall name LAN_IN")); ok {
		t.Fatal("LAN_IN still there")
	}
	if srv.Stats().Commits != 3 {
		t.Fatalf("stats %+v", srv.Stats())
	}
}

func leaves(kv ...any) configtree.Leaves {
	l := configtree.NewLeaves()
	for i := 0; i < len(kv); i += 2 {
		l.Set(p(kv[i].(string)), kv[i+1].(configtree.Value))
	}
	return l
}

func TestApplyErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		prep      func(*fakeedgeos.Server)
		batch     func() *configtree.Batch
		wantStage string
		wantMsg   string
	}{
		{
			name: "set validation",
			batch: func() *configtree.Batch {
				b := configtree.NewBatch()
				b.Set(p("firewall name X rule 10 action"), configtree.Single("explode"))
				return b
			},
			wantStage: "SET",
			wantMsg:   `firewall name X rule 10 action: Value validation failed: "explode"`,
		},
		{
			name: "missing firewall reference",
			batch: func() *configtree.Batch {
				b := configtree.NewBatch()
				b.Set(p("interfaces ethernet eth0 firewall in name"), configtree.Single("MISSING"))
				return b
			},
			wantStage: "COMMIT",
			wantMsg:   "interfaces ethernet eth0 firewall in: Firewall name [MISSING] does not exist",
		},
		{
			name: "injected commit failure",
			prep: func(s *fakeedgeos.Server) { s.FailNextCommit("iptables-restore: line 2 failed") },
			batch: func() *configtree.Batch {
				b := configtree.NewBatch()
				b.Set(p("system host-name"), configtree.Single("r2"))
				return b
			},
			wantStage: "COMMIT",
			wantMsg:   "iptables-restore: line 2 failed",
		},
		{
			name: "delete missing node",
			batch: func() *configtree.Batch {
				b := configtree.NewBatch()
				b.Delete(p("firewall name GHOST"))
				return b
			},
			wantStage: "DELETE",
			wantMsg:   "firewall name GHOST: Nothing to delete",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, tr := newFake(t, fakeedgeos.Options{})
			before := srv.Tree()
			if tc.prep != nil {
				tc.prep(srv)
			}
			err := tr.Apply(context.Background(), tc.batch())
			var ce *client.CommitError
			if !errors.As(err, &ce) {
				t.Fatalf("err = %v, want CommitError", err)
			}
			if ce.Stage != tc.wantStage {
				t.Fatalf("stage = %s, want %s (%v)", ce.Stage, tc.wantStage, err)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("error %q does not contain %q", err, tc.wantMsg)
			}
			if !reflect.DeepEqual(before, srv.Tree()) {
				t.Fatal("a failed batch changed the config")
			}
		})
	}
}

func TestSessionExpiry(t *testing.T) {
	t.Parallel()
	srv, tr := newFake(t, fakeedgeos.Options{})
	ctx := context.Background()
	if _, _, err := tr.Read(ctx, p("system host-name")); err != nil {
		t.Fatal(err)
	}
	srv.ExpireSessions()
	b := configtree.NewBatch()
	b.Set(p("system host-name"), configtree.Single("after-expiry"))
	if err := tr.Apply(ctx, b); err != nil {
		t.Fatal(err)
	}
	if tr.Logins() != 2 {
		t.Fatalf("logins = %d, want 2", tr.Logins())
	}
	if v, _ := srv.Get(p("system host-name")); v != "after-expiry" {
		t.Fatalf("host-name = %v", v)
	}
	// Exactly one batch reached the server: the 403 was not executed.
	if srv.Stats().Commits != 1 {
		t.Fatalf("stats %+v", srv.Stats())
	}
}

func TestSystemInfo(t *testing.T) {
	t.Parallel()
	_, tr := newFake(t, fakeedgeos.Options{SoftwareVersion: "EdgeRouter.ER-e300.v2.0.9-hotfix.7.5622731.230615.0857"})
	info, err := tr.SystemInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Platform != "e300" || info.Version != "v2.0.9-hotfix" || info.Build != "7.5622731.230615.0857" {
		t.Fatalf("info = %+v", info)
	}
}

// stub serves canned responses after a successful login.
func stub(t *testing.T, handler http.HandlerFunc) *webapi.Transport {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" && r.Method == http.MethodPost {
			http.SetCookie(w, &http.Cookie{Name: "PHPSESSID", Value: "s", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "X-CSRF-TOKEN", Value: "c", Path: "/"})
			w.Header().Set("Location", "/")
			w.WriteHeader(http.StatusSeeOther)
			return
		}
		handler(w, r)
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	tr, err := webapi.New(webapi.Config{Host: srv.URL, Username: "u", Password: "p", Insecure: true})
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func TestTransportOddResponses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		status  int
		ctype   string
		body    string
		call    func(*webapi.Transport) error
		wantErr string
	}{
		{"server error", 500, "text/plain", "boom", readHost, "HTTP 500: boom"},
		{"html instead of json", 200, "text/html", "<html>", readHost, "session expired"},
		{"invalid json", 200, "application/json", "{", readHost, "invalid JSON"},
		{"partial failure", 200, "application/json", `{"success": false}`, readHost, "partial.json"},
		{"batch invalid json", 200, "application/json", "nope", applySome, "invalid JSON"},
		{"sysinfo invalid", 200, "application/json", "x", sysInfo, "invalid JSON"},
		{"sysinfo empty", 200, "application/json", `{"success":"0"}`, sysInfo, "sys_info failed"},
		{"redirect means login", 302, "text/html", "", readHost, "session expired"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			calls := 0
			tr := stub(t, func(w http.ResponseWriter, _ *http.Request) {
				mu.Lock()
				calls++
				mu.Unlock()
				w.Header().Set("Content-Type", tc.ctype)
				if tc.status == 302 {
					w.Header().Set("Location", "/")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			err := tc.call(tr)
			if tc.wantErr == "session expired" {
				// The transport logs in again and retries exactly once.
				mu.Lock()
				defer mu.Unlock()
				if calls != 2 {
					t.Fatalf("calls = %d, want 2", calls)
				}
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func readHost(tr *webapi.Transport) error {
	_, _, err := tr.Read(context.Background(), p("system host-name"))
	return err
}

func applySome(tr *webapi.Transport) error {
	b := configtree.NewBatch()
	b.Set(p("system host-name"), configtree.Single("x"))
	return tr.Apply(context.Background(), b)
}

func sysInfo(tr *webapi.Transport) error {
	_, err := tr.SystemInfo(context.Background())
	return err
}

func TestUnreachable(t *testing.T) {
	t.Parallel()
	tr, err := webapi.New(webapi.Config{Host: "127.0.0.1", Port: 1, Username: "u", Password: "p", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := readHost(tr); err == nil || !strings.Contains(err.Error(), "connecting to") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoginUnexpectedStatus(t *testing.T) {
	t.Parallel()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(srv.Close)
	tr, _ := webapi.New(webapi.Config{Host: srv.URL, Username: "u", Password: "p", Insecure: true})
	if err := tr.Login(context.Background()); err == nil || !strings.Contains(err.Error(), "HTTP 418") {
		t.Fatalf("err = %v", err)
	}

	noCookie := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "/")
		w.WriteHeader(http.StatusSeeOther)
	}))
	t.Cleanup(noCookie.Close)
	tr, _ = webapi.New(webapi.Config{Host: noCookie.URL, Username: "u", Password: "p", Insecure: true, UserAgent: "test"})
	if err := tr.Login(context.Background()); err == nil || !strings.Contains(err.Error(), "session cookie") {
		t.Fatalf("err = %v", err)
	}
}

func TestCAPinning(t *testing.T) {
	t.Parallel()
	srv := fakeedgeos.New(fakeedgeos.Options{})
	srv.Start()
	t.Cleanup(srv.Close)
	_, port := srv.HostPort()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	// The httptest certificate is issued for 127.0.0.1, so verification
	// works without skipping it.
	tr, err := webapi.New(webapi.Config{Host: "127.0.0.1", Port: atoi(t, port), Username: fakeedgeos.DefaultUsername, Password: fakeedgeos.DefaultPassword, CACertPEM: ca})
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Login(context.Background()); err != nil {
		t.Fatalf("login with pinned CA: %v", err)
	}
}
