/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package v1

import "testing"

// Run without envtest: go test -run TestMergeAllowedAuthorities ./internal/webhook/v1/
func TestMergeAllowedAuthorities(t *testing.T) {
	cases := []struct {
		name       string
		existing   string
		exists     bool
		host       string
		wantValue  string
		wantSetEnv bool
	}{
		{
			name:       "unset + custom host => defaults plus host",
			exists:     false,
			host:       "decofiles.example.com",
			wantValue:  "configs.decocdn.com,configs.deco.cx,admin.deco.cx,localhost,decofiles.example.com",
			wantSetEnv: true,
		},
		{
			name:       "unset + host already a default => no env needed",
			exists:     false,
			host:       "configs.decocdn.com",
			wantSetEnv: false,
		},
		{
			name:       "existing without host => appended, existing preserved",
			existing:   "foo.com,bar.com",
			exists:     true,
			host:       "decofiles.example.com",
			wantValue:  "foo.com,bar.com,decofiles.example.com",
			wantSetEnv: true,
		},
		{
			name:       "existing already contains host => unchanged (still written to preserve)",
			existing:   "foo.com,decofiles.example.com",
			exists:     true,
			host:       "decofiles.example.com",
			wantValue:  "foo.com,decofiles.example.com",
			wantSetEnv: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, setEnv := mergeAllowedAuthorities(tc.existing, tc.exists, tc.host)
			if setEnv != tc.wantSetEnv {
				t.Fatalf("setEnv = %v, want %v", setEnv, tc.wantSetEnv)
			}
			if setEnv && value != tc.wantValue {
				t.Fatalf("value = %q, want %q", value, tc.wantValue)
			}
		})
	}
}
