// Package notices exposes the embedded third-party attribution
// text that the geblang and gebweb CLIs surface via their
// `licenses` subcommands. The adjacent NOTICES.md is embedded in
// the binaries and mirrored by the repo-root NOTICES.md for
// distributors.
package notices

import _ "embed"

//go:embed NOTICES.md
var Text string
