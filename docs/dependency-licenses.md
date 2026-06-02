# Dependency License Notes

Practical license audit for the Go dependencies in `go.mod` as of
geblang 1.6.0 development. Not legal advice; get counsel before a
commercial release if the distribution model matters.

## Summary

Geblang can be kept closed source if desired. The current dependency
set does not include GPL, LGPL, AGPL, SSPL, or other strong copyleft
licenses.

The only weak-copyleft dependency is MPL-2.0:

- `github.com/go-sql-driver/mysql`

MPL-2.0 is file-level copyleft. In ordinary use, it permits closed-
source applications that depend on the library, but if you modify
MPL-covered source files you must make those modified MPL-covered
files available under MPL-2.0. Preserve notices and license text when
distributing binaries.

Most other dependencies are permissive: MIT, BSD-style, or Apache-2.0.

## Per-dependency audit

Each entry below lists the import path, version, SPDX licence id, and
the copyright line as it appears in the upstream LICENSE file.
Sorted by import path; direct dependencies first, then indirect.

### Direct

| Import path | Version | SPDX | Copyright |
|-------------|---------|------|-----------|
| `github.com/BurntSushi/toml` | v1.6.0 | MIT | Copyright (c) 2013 TOML authors |
| `github.com/go-sql-driver/mysql` | v1.10.0 | MPL-2.0 | The Go MySQL Driver Authors |
| `github.com/google/uuid` | v1.6.0 | BSD-3-Clause | Copyright (c) 2009, 2014 Google Inc. All rights reserved |
| `github.com/gorilla/websocket` | v1.5.3 | BSD-2-Clause | Copyright (c) 2013 The Gorilla WebSocket Authors. All rights reserved |
| `github.com/jackc/pgx/v5` | v5.9.2 | MIT | Copyright (c) 2013-2021 Jack Christensen |
| `github.com/yuin/goldmark` | v1.8.2 | MIT | Copyright (c) 2019 Yusuke Inuzuka |
| `github.com/yuin/goldmark-emoji` | v1.0.6 | MIT | Copyright (c) 2020 Yusuke Inuzuka |
| `golang.org/x/crypto` | v0.52.0 | BSD-3-Clause | Copyright 2009 The Go Authors |
| `golang.org/x/sys` | v0.45.0 | BSD-3-Clause | Copyright 2009 The Go Authors |
| `gopkg.in/yaml.v3` | v3.0.1 | MIT and Apache-2.0 (dual) | Copyright (c) 2006-2011 Kirill Simonov; Copyright (c) 2011-2019 Canonical Ltd |
| `modernc.org/sqlite` | v1.50.1 | BSD-3-Clause | Copyright (c) 2017 The Sqlite Authors. All rights reserved |

### Indirect

| Import path | Version | SPDX | Copyright |
|-------------|---------|------|-----------|
| `filippo.io/edwards25519` | v1.2.0 | BSD-3-Clause | Copyright (c) 2009 The Go Authors. All rights reserved |
| `github.com/creack/pty` | v1.1.24 | MIT (Expat-style) | Copyright (c) 2011 Keith Rarick |
| `github.com/dlclark/regexp2` | v1.12.0 | MIT | Copyright (c) Doug Clark |
| `github.com/dustin/go-humanize` | v1.0.1 | MIT (Expat-style) | Copyright (c) 2005-2008 Dustin Sallings |
| `github.com/ebitengine/purego` | v0.10.1 | Apache-2.0 | The Ebitengine Authors |
| `github.com/fsnotify/fsnotify` | v1.10.1 | BSD-3-Clause | Copyright (c) 2012 The Go Authors; Copyright (c) fsnotify Authors. All rights reserved |
| `github.com/jackc/pgpassfile` | v1.0.0 | MIT | Copyright (c) 2019 Jack Christensen |
| `github.com/jackc/pgservicefile` | v0.0.0-20240606120523 | MIT | Copyright (c) 2020 Jack Christensen |
| `github.com/jackc/puddle/v2` | v2.2.2 | MIT | Copyright (c) 2018 Jack Christensen |
| `github.com/klauspost/compress` | v1.15.9 | BSD-3-Clause | Copyright (c) 2012 The Go Authors; Copyright (c) 2019 Klaus Post. All rights reserved |
| `github.com/kr/fs` | v0.1.0 | BSD-3-Clause | Copyright (c) 2012 The Go Authors. All rights reserved |
| `github.com/kr/text` | v0.2.0 | MIT (Expat-style) | Copyright 2012 Keith Rarick |
| `github.com/mattn/go-isatty` | v0.0.22 | MIT | Copyright (c) Yasuhiro MATSUMOTO |
| `github.com/ncruces/go-strftime` | v1.0.0 | MIT | Copyright (c) 2022 Nuno Cruces |
| `github.com/pierrec/lz4/v4` | v4.1.15 | BSD-3-Clause | Copyright (c) 2015, Pierre Curto. All rights reserved |
| `github.com/pkg/sftp` | v1.13.10 | BSD-2-Clause | Copyright (c) 2013, Dave Cheney. All rights reserved |
| `github.com/rabbitmq/amqp091-go` | v1.11.0 | BSD-2-Clause | Copyright (c) 2021 VMware, Inc. or its affiliates. All Rights Reserved |
| `github.com/remyoudompheng/bigfft` | v0.0.0-20230129092748 | BSD-3-Clause | Copyright (c) 2012 The Go Authors. All rights reserved |
| `github.com/rogpeppe/go-internal` | v1.14.1 | BSD-3-Clause | Copyright (c) 2018 The Go Authors. All rights reserved |
| `github.com/segmentio/kafka-go` | v0.4.51 | MIT | Copyright (c) 2017 Segment |
| `golang.org/x/sync` | v0.20.0 | BSD-3-Clause | Copyright 2009 The Go Authors |
| `golang.org/x/text` | v0.37.0 | BSD-3-Clause | Copyright 2009 The Go Authors |
| `modernc.org/gc/v3` | v3.1.3 | BSD-3-Clause | Copyright (c) 2016 The GC Authors. All rights reserved |
| `modernc.org/libc` | v1.72.3 | BSD-3-Clause | Copyright (c) 2017 The Libc Authors. All rights reserved |
| `modernc.org/mathutil` | v1.7.1 | BSD-3-Clause | Copyright (c) 2014 The mathutil Authors. All rights reserved |
| `modernc.org/memory` | v1.11.0 | BSD-3-Clause | Copyright (c) 2017 The Memory Authors. All rights reserved |
| `software.sslmate.com/src/go-pkcs12` | v0.7.1 | BSD-3-Clause | Copyright (c) 2015, 2018, 2019 Opsmate, Inc. All rights reserved |

## Licence summary

| SPDX | Direct + indirect count | Obligation for a binary distribution |
|------|-------------------------|--------------------------------------|
| MIT (and MIT/Expat-style) | 14 | Include the copyright notice and licence text with any copy. |
| BSD-2-Clause | 3 | Reproduce the copyright notice, conditions list, and disclaimer in materials accompanying the binary. |
| BSD-3-Clause | 19 | As BSD-2 plus: do not use contributor names to endorse derivatives without permission. |
| Apache-2.0 | 1 + 1 dual | Include the licence text; preserve any NOTICE file the dep ships; note any modifications. |
| MPL-2.0 | 1 | Include the licence text; make the source of modified MPL-covered files available. Unmodified consumption: source remains available at the upstream module URL. |

## Practical obligations

Distributing Geblang binaries:

- Ship `NOTICES.md` alongside the binary (or embed and surface via
  `geblang licenses` / `gebweb licenses`).
- Preserve copyright notices in the source tree.
- Track any modifications to MPL-2.0 dependencies and publish those
  modified MPL-covered files if you distribute them.
- Re-run this audit when dependencies are added, removed, or
  upgraded. A one-liner to refresh the table is in
  `scripts/audit-licenses.sh` (TODO).

## Canonical licence text URLs

For assembling `NOTICES.md`:

- MIT: <https://spdx.org/licenses/MIT.html>
- BSD-2-Clause: <https://spdx.org/licenses/BSD-2-Clause.html>
- BSD-3-Clause: <https://spdx.org/licenses/BSD-3-Clause.html>
- Apache-2.0: <https://www.apache.org/licenses/LICENSE-2.0.txt>
- MPL-2.0: <https://www.mozilla.org/MPL/2.0/>
