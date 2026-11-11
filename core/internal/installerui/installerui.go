// Package installerui keeps the built files of the installer page
// (milestone M-07, decision D32). The page is a second entry of the
// console project (console/installer.html), thus it shares the design
// tokens and the parts of the console.
package installerui

import (
	"embed"
	"io/fs"

	"github.com/fathorMB/NidaOS/core/internal/console"
)

//go:embed all:dist
var files embed.FS

// New gives the handler of the page.
func New() *console.Handler {
	sub, err := fs.Sub(files, "dist")
	if err != nil {
		panic(err)
	}
	return console.NewFSIndex(sub, "installer.html")
}
