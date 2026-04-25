package main

import (
	"reflect"
	"testing"
)

func TestRewriteVersionAlias(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "no args",
			in:   []string{"tezsign-host"},
			want: []string{"tezsign-host"},
		},
		{
			name: "existing command unchanged",
			in:   []string{"tezsign-host", "version"},
			want: []string{"tezsign-host", "version"},
		},
		{
			name: "top-level version alias",
			in:   []string{"tezsign-host", "--version"},
			want: []string{"tezsign-host", "version"},
		},
		{
			name: "version after long device flag",
			in:   []string{"tezsign-host", "--device", "abc123", "--version"},
			want: []string{"tezsign-host", "--device", "abc123", "version"},
		},
		{
			name: "version after short device flag",
			in:   []string{"tezsign-host", "-d", "abc123", "--version"},
			want: []string{"tezsign-host", "-d", "abc123", "version"},
		},
		{
			name: "version after device equals syntax",
			in:   []string{"tezsign-host", "--device=abc123", "--version"},
			want: []string{"tezsign-host", "--device=abc123", "version"},
		},
		{
			name: "version after command stays unchanged",
			in:   []string{"tezsign-host", "status", "--version"},
			want: []string{"tezsign-host", "status", "--version"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := rewriteVersionAlias(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("rewriteVersionAlias(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
