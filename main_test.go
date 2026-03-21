package main

import (
	"runtime/debug"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		explicit string
		info     *debug.BuildInfo
		ok       bool
		want     string
	}{
		{
			name:     "explicit build flag wins",
			explicit: "v0.1.6",
			want:     "v0.1.6",
		},
		{
			name: "module version",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "v0.1.6"},
			},
			ok:   true,
			want: "v0.1.6",
		},
		{
			name: "vcs revision",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "(devel)"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "0123456789abcdef"},
				},
			},
			ok:   true,
			want: "0123456789ab",
		},
		{
			name: "dirty vcs revision",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "(devel)"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "0123456789abcdef"},
					{Key: "vcs.modified", Value: "true"},
				},
			},
			ok:   true,
			want: "0123456789ab-dirty",
		},
		{
			name: "devel fallback",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "(devel)"},
			},
			ok:   true,
			want: "(devel)",
		},
		{
			name: "unknown without build info",
			ok:   false,
			want: "(unknown)",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := resolveVersion(tc.explicit, tc.info, tc.ok)
			if got != tc.want {
				t.Fatalf("resolveVersion(%q, %+v, %v) = %q, want %q", tc.explicit, tc.info, tc.ok, got, tc.want)
			}
		})
	}
}
