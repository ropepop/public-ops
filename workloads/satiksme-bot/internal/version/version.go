package version

import "fmt"

var (
	Commit       = "dev"
	BuildTime    = "unknown"
	Dirty        = "unknown"
	ReleaseID    = "unknown"
	SourceSHA256 = "unknown"
)

func Display() string {
	return fmt.Sprintf("%s (%s, %s)", Commit, BuildTime, Dirty)
}
