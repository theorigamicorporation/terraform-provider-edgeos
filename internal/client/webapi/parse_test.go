// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package webapi

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/theorigamicorporation/terraform-provider-edgeos/internal/client"
)

func TestParseBatchResponse(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		body      string
		wantStage string
		want      []client.PathError
		wantErr   bool
	}{
		{
			name: "all good",
			body: `{"SET":{"failure":"0","success":"1"},"COMMIT":{"failure":"0","success":"1"},"SAVE":{"success":"1"},"success":true}`,
		},
		{
			name: "numeric status",
			body: `{"DELETE":{"failure":0,"success":1},"COMMIT":{"failure":0,"success":1}}`,
		},
		{
			name:      "set error map",
			body:      `{"SET":{"failure":"1","success":"0","error":{"firewall name X rule 10 action":"Value validation failed\n￿0"}}}`,
			wantStage: "SET",
			want:      []client.PathError{{Path: "firewall name X rule 10 action", Message: "Value validation failed"}},
		},
		{
			name:      "set error map skips successes",
			body:      `{"SET":{"failure":"1","error":{"a":"fine￿1","b":"bad"}}}`,
			wantStage: "SET",
			want:      []client.PathError{{Path: "b", Message: "bad"}},
		},
		{
			name:      "commit error string",
			body:      "{\"SET\":{\"success\":\"1\"},\"COMMIT\":{\"failure\":\"1\",\"success\":\"0\",\"error\":\"[ firewall name X ]\\nRule action [REQUIRED]\\n\\nCommit failed\\uFFFF0\\uFFFE[ interfaces ethernet eth0 ]\\nok\\uFFFF1\"}}",
			wantStage: "COMMIT",
			want:      []client.PathError{{Path: "firewall name X", Message: "Rule action [REQUIRED] Commit failed"}},
		},
		{
			name:      "commit error without path",
			body:      `{"COMMIT":{"failure":"1","error":"iptables-restore: line 2 failed\n"}}`,
			wantStage: "COMMIT",
			want:      []client.PathError{{Message: "iptables-restore: line 2 failed"}},
		},
		{
			name:      "failure without details",
			body:      `{"COMMIT":{"failure":"1"}}`,
			wantStage: "COMMIT",
			want:      []client.PathError{{Message: "unspecified commit error"}},
		},
		{
			name:      "success zero counts as failure",
			body:      `{"SAVE":{"success":"0","error":["disk full"]}}`,
			wantStage: "SAVE",
			want:      []client.PathError{{Message: `["disk full"]`}},
		},
		{
			name:      "overall failure",
			body:      `{"success":false}`,
			wantStage: "BATCH",
		},
		{
			name:    "no stages",
			body:    `{"success":true}`,
			wantErr: true,
		},
		{
			name:    "not json",
			body:    `<html>`,
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ParseBatchResponse([]byte(tc.body))
			if tc.wantStage == "" && !tc.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if tc.wantErr {
				var ce *client.CommitError
				if err == nil || errors.As(err, &ce) {
					t.Fatalf("want a plain error, got %v", err)
				}
				return
			}
			var ce *client.CommitError
			if !errors.As(err, &ce) {
				t.Fatalf("want CommitError, got %v", err)
			}
			if ce.Stage != tc.wantStage {
				t.Fatalf("stage %s, want %s", ce.Stage, tc.wantStage)
			}
			if tc.want != nil && !reflect.DeepEqual(ce.Errors, tc.want) {
				t.Fatalf("errors %#v, want %#v", ce.Errors, tc.want)
			}
		})
	}
}

func TestTruthy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		def  bool
		want bool
	}{
		{`"1"`, false, true},
		{`"0"`, true, false},
		{`1`, false, true},
		{`0`, true, false},
		{`true`, false, true},
		{`false`, true, false},
		{`"true"`, false, true},
		{`"no"`, true, false},
		{`""`, true, false},
		{`"maybe"`, true, true},
		{`null`, true, true},
		{``, false, false},
		{`{`, true, true},
		{`[]`, false, false},
	}
	for _, tc := range cases {
		if got := truthy(json.RawMessage(tc.raw), tc.def); got != tc.want {
			t.Errorf("truthy(%s, %v) = %v", tc.raw, tc.def, got)
		}
	}
}
