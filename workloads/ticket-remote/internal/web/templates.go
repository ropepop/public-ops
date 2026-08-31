package web

import "io/fs"

var (
	indexHTML         = mustReadTemplate("static/index.html.tmpl")
	authRedirectHTML  = mustReadTemplate("static/auth-redirect.html.tmpl")
	adminHTML         = mustReadTemplate("static/admin.html.tmpl")
	hdrDiagnosticHTML = mustReadTemplate("diagnostic/hdr-diagnostic.html.tmpl")
)

func mustReadTemplate(name string) string {
	raw, err := fs.ReadFile(staticFS, name)
	if err != nil {
		panic(err)
	}
	return string(raw)
}
