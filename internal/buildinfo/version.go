package buildinfo

import "runtime/debug"

var rawVersion = "dev"

// Version returns the release version injected at build time, the module
// version when installed with Go, or "dev" for an unversioned checkout.
func Version() string {
	return Resolve(rawVersion)
}

// Resolve applies the same version fallback to an explicitly supplied value.
func Resolve(value string) string {
	if value != "" && value != "dev" {
		return value
	}

	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return value
	}
	return info.Main.Version
}
