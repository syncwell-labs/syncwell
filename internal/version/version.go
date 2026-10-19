package version

import "fmt"

// These values are filled by release builds through -ldflags. The defaults keep
// local development builds useful even when no VCS metadata is injected.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

func String() string {
	return fmt.Sprintf("syncwell %s (commit %s, built %s)", Version, Commit, Date)
}
