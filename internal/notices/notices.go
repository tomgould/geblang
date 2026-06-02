// Package notices exposes the embedded third-party attribution
// text that the geblang and gebweb CLIs surface via their
// `licenses` subcommands. The source of truth is the adjacent
// NOTICES.md; the repo-root NOTICES.md is a redirect that links
// readers back here.
package notices

import _ "embed"

//go:embed NOTICES.md
var Text string
