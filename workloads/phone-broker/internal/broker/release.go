package broker

import (
	"net/http"

	phonebrokerversion "phonebroker/internal/version"
)

func setReleaseHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Release-Commit", phonebrokerversion.Commit)
	w.Header().Set("X-Release-Build-Time", phonebrokerversion.BuildTime)
	w.Header().Set("X-Release-Dirty", phonebrokerversion.Dirty)
	w.Header().Set("X-Release-Id", phonebrokerversion.ReleaseID)
	w.Header().Set("X-Release-Source-Sha256", phonebrokerversion.SourceSHA256)
}
