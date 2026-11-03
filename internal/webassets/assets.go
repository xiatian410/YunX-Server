package webassets

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var content embed.FS

func FS() fs.FS { f, _ := fs.Sub(content, "dist"); return f }
