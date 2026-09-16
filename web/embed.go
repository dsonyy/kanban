// Package web holds the browser client: Go templates and the Vite build in dist/, embedded into the server binary.
package web

import "embed"

// dist is produced by `npm run build` in this directory; `just build` runs it before compiling Go.
//
//go:embed templates all:dist
var FS embed.FS
