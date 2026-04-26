package version

import "fmt"

var (
	Commit    = "dev"
	BuildTime = "unknown"
	Dirty     = "unknown"
)

func Display() string {
	return fmt.Sprintf("commit=%s build=%s dirty=%s", Commit, BuildTime, Dirty)
}
