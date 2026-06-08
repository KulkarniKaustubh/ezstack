package main

import "testing"

func TestResolveMCPRepo(t *testing.T) {
	tests := []struct {
		name       string
		flag, env  string
		wantPath   string
		wantSource string
	}{
		{name: "flag wins over env", flag: "/x", env: "/e", wantPath: "/x", wantSource: "--repo"},
		{name: "flag only", flag: "/x", wantPath: "/x", wantSource: "--repo"},
		{name: "env fallback", env: "/e", wantPath: "/e", wantSource: "EZSTACK_REPO"},
		{name: "neither", wantPath: "", wantSource: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, source := resolveMCPRepo(tt.flag, tt.env)
			if path != tt.wantPath || source != tt.wantSource {
				t.Errorf("resolveMCPRepo(%q, %q) = (%q, %q), want (%q, %q)",
					tt.flag, tt.env, path, source, tt.wantPath, tt.wantSource)
			}
		})
	}
}
