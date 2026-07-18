// Package buildinfo exposes the version embedded in Manager and Agent builds.
package buildinfo

import "strings"

// Version is overridden by release builds with:
// -ldflags "-X gmha/internal/buildinfo.Version=Vx.y.z"
var Version = "V0.0.1"

func CurrentVersion() string {
	version := strings.TrimSpace(Version)
	if version == "" {
		return "V0.0.1"
	}
	if version[0] == 'v' {
		return "V" + version[1:]
	}
	if version[0] != 'V' {
		return "V" + version
	}
	return version
}
