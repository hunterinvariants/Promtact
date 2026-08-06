// Package web embeds the built console so the product still ships as a single
// binary: no separate asset directory has to be deployed alongside it.
//
// The assets are produced by `npm run build` in ../ui and committed under dist,
// so `go build` works without a Node toolchain.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var assets embed.FS

// Console returns the embedded console file system rooted at the build output.
func Console() (fs.FS, error) {
	return fs.Sub(assets, "dist")
}
