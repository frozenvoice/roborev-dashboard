package webui

import "embed"

// Files contains the Vite production build. The checked-in placeholder keeps
// backend tests buildable before npm has produced the real assets.
//
//go:embed all:dist
var Files embed.FS
