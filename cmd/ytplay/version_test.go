package main

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestVersionStringSaysVersionAndPlatform(t *testing.T) {
	old := version
	defer func() { version = old }()
	version = "v1.2.3"
	got := versionString()
	if !strings.HasPrefix(got, "ytplay v1.2.3 (go") || !strings.Contains(got, "/") {
		t.Errorf("versionString() = %q", got)
	}
}

func TestVersionWithoutALdflagComesFromTheBuildInfo(t *testing.T) {
	for name, tc := range map[string]struct {
		info *debug.BuildInfo
		want string
	}{
		"module tag":    {&debug.BuildInfo{Main: debug.Module{Version: "v0.3.1"}}, "v0.3.1"},
		"checkout":      {&debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "0123456789abcdef"}}}, "dev-0123456"},
		"dirty":         {&debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "0123456789abcdef"}, {Key: "vcs.modified", Value: "true"}}}, "dev-0123456-dirty"},
		"no vcs":        {&debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, "dev"},
		"short rev":     {&debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}}}, "dev-abc"},
		"empty version": {&debug.BuildInfo{}, "dev"},
	} {
		if got := fromBuildInfo(tc.info); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}
