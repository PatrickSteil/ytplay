package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// version is set at build time (-ldflags "-X main.version=v0.1.0") by the
// Makefile and by scripts/build-release.sh. Left as "dev" it is looked up in
// the build information instead, which holds the module version of a
// `go install ...@v0.1.0` and the commit of a build from a checkout.
var version = "dev"

// versionString is the text of `ytplay -version`.
func versionString() string {
	v := version
	var info *debug.BuildInfo
	if bi, ok := debug.ReadBuildInfo(); ok {
		info = bi
	}
	if v == "dev" && info != nil {
		v = fromBuildInfo(info)
	}
	return fmt.Sprintf("ytplay %s (%s, %s/%s)", v, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

// fromBuildInfo is the version a build of a checkout or a module reports:
// the module's tag if it has one, else "dev" with the short commit (and
// "-dirty" if the tree had changes).
func fromBuildInfo(info *debug.BuildInfo) string {
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	var rev, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	if rev == "" {
		return "dev"
	}
	return "dev-" + rev[:min(len(rev), 7)] + dirty
}
