// Package web embeds the static frontend into the binary. The frontend
// is plain HTML/CSS/ES modules — no build step, no Node.js toolchain.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var static embed.FS

// Static returns the embedded frontend files rooted at the static dir.
func Static() (fs.FS, error) {
	return fs.Sub(static, "static")
}
