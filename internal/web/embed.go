package web

import "embed"

//go:embed templates/*.html static/css/*.css static/js/*.js static/*.svg content/*.md
var embedded embed.FS
