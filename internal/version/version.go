// Package version holds the build version, set at link time.
package version

// Version is overridden with -ldflags "-X github.com/danielfnfaria/agtop/internal/version.Version=...".
var Version = "dev"
