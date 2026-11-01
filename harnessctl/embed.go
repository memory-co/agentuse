// Package harnessctl embeds independently distributed CLI packages for installers.
// The packages themselves have no dependency on this Go module.
package harnessctl

import "embed"

// Files contains only distributable adapter files, never tests or runtime state.
//
//go:embed harness/*/harnessctl harness/*/manifest.json harness/*/event.schema.json harness/*/EVENT.md harness/*/README.md harness/*/LICENSE harness/*/lib/*.py
var Files embed.FS
