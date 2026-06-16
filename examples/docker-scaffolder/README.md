# Docker scaffolder

An interactive CLI, written in Geblang, that scaffolds a Docker / Compose
project. It showcases the `cli` prompt surface (including the
`cli.multiChoose` checkbox UI), `yaml.stringify` for the Compose files,
and `io.withStdin` for testing an interactive program end to end.

## Run

```sh
geblang examples/docker-scaffolder/src/main.gb [--out DIR]
```

It will:

1. Check that Docker and Compose (`docker compose` or `docker-compose`)
   are on `PATH` (a missing toolchain is a note, not a failure).
2. Prompt for a project name, the languages to include (multi-select:
   PHP, Python, Node, Go, Geblang), per-language version / runtime / port,
   optional CPU / memory limits, GPU access and a reverse-proxy hostname,
   then a database and optional Redis cache and Mailpit mail catcher.

   PHP offers four architectures: **Nginx** (php-fpm fronted by a generated
   nginx service), **Apache** (mod_php), **CLI** (no web server), and
   **FrankenPHP** (which also publishes 443 for its automatic, self-signed
   localhost HTTPS). Geblang services build on the official
   `dwgebler/geblang` image.
3. Write into `DIR` (default: the project name):
   - `compose.yaml` (base), `compose.override.yaml` (dev),
     `compose.prod.yaml` (prod, with resource limits)
   - `docker/<service>/Dockerfile` for each language service
   - `docker/nginx/default.conf` when any service has a proxy hostname
     (and the `/etc/hosts` lines to add are printed)

## What it generates

The templates follow current container best practices:

- Spec-style `compose.yaml` (no top-level `version`), a shared network,
  named volumes, `restart` policies, database healthchecks
  (`pg_isready` / `mysqladmin ping` / `mongosh`), and app services that
  `depends_on` the database with `condition: service_healthy`.
- Multi-stage, non-root Dockerfiles on slim / alpine bases: a `composer`
  vendor stage for PHP (or FrankenPHP), a venv builder for Python,
  `npm ci` deps stage for Node, and a `distroless/static` final image for
  Go built with `CGO_ENABLED=0`.
- Optional add-ons: a Redis cache (`redis:8-alpine`) and Mailpit mail
  catcher; per-service GPU reservations (`deploy.resources.reservations`,
  with a CUDA base image for Python) and CPU / memory limits; and an
  nginx reverse proxy that `proxy_pass`es HTTP services or `fastcgi_pass`es
  PHP-FPM, with the `/etc/hosts` entries printed for you.

Image versions track current stable releases (PHP 8.5, Python 3.14,
Node 24 LTS, Go 1.26, PostgreSQL 18, MySQL 8.4, MariaDB 12, MongoDB 8).

## Layout

| File | Role |
|------|------|
| `src/model.gb` | `Project` / `Service` config types |
| `src/prompts.gb` | interactive gathering (`cli.prompt` / `multiChoose` / `choose`) |
| `src/templates.gb` | Dockerfile + Compose generation and file writing |
| `src/main.gb` | Docker detection, orchestration, output |
| `src/scaffolder_test.gb` | end-to-end + generation tests (scripted via `io.withStdin`) |
| `src/templates_test.gb` | same-module test of private helpers |
