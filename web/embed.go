// Package web embeds the built SolidJS bundle so the server ships as a single
// binary. In development the directory holds only a placeholder and the
// frontend is served by Vite instead.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the bundle rooted at dist/.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist/ is embedded above; a failure here is a build bug
	}
	return sub
}
