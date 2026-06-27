package web

import (
	"net/http"

	ticketremoteversion "ticketremote/internal/version"
)

func setReleaseHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Release-Commit", ticketremoteversion.Commit)
	w.Header().Set("X-Release-Build-Time", ticketremoteversion.BuildTime)
	w.Header().Set("X-Release-Dirty", ticketremoteversion.Dirty)
	w.Header().Set("X-Release-Id", ticketremoteversion.ReleaseID)
	w.Header().Set("X-Release-Source-Sha256", ticketremoteversion.SourceSHA256)
}
