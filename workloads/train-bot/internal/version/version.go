package version

import (
	"fmt"
	"strings"
)

var (
	Commit       = "dev"
	BuildTime    = "unknown"
	Dirty        = "unknown"
	ReleaseID    = "unknown"
	SourceSHA256 = "unknown"
)

func ShortCommit() string {
	if len(Commit) > 12 {
		return Commit[:12]
	}
	return Commit
}

func IsDirty() bool {
	return strings.EqualFold(Dirty, "dirty")
}

func Display() string {
	state := "clean"
	if IsDirty() {
		state = "dirty"
	}
	return fmt.Sprintf("v:%s • %s • built %s", ShortCommit(), state, BuildTime)
}
