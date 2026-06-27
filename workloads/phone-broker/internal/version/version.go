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
	return fmt.Sprintf("commit=%s build=%s dirty=%s release=%s source_sha256=%s", Commit, BuildTime, Dirty, ReleaseID, SourceSHA256)
}
