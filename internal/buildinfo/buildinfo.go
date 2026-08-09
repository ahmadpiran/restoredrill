// Package buildinfo holds the build version, injected via -ldflags (see
// .goreleaser.yaml). Shared by --version and the report so they can't diverge.
package buildinfo

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)
