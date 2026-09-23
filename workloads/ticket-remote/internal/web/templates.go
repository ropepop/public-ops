package web

import (
	"html/template"
	"io/fs"
)

var (
	indexHTML         = mustReadTemplate("static/index.html.tmpl")
	adminHTML         = mustReadTemplate("static/admin.html.tmpl")
	hdrDiagnosticHTML = mustReadTemplate("diagnostic/hdr-diagnostic.html.tmpl")
	welcomeTmpl       = template.Must(template.New("welcome").Parse(mustReadTemplate("welcome/index.html.tmpl")))
)

func mustReadTemplate(name string) string {
	raw, err := fs.ReadFile(staticFS, name)
	if err != nil {
		panic(err)
	}
	return string(raw)
}
