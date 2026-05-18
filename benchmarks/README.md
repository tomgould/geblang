# Geblang Benchmarks

This directory contains small, repeatable benchmarks for tracking Geblang
runtime performance against familiar scripting-language baselines.

Run all benchmarks:

```sh
make bench
```

Run PHP and Python comparisons inside small Docker CLI containers:

```sh
make bench-docker
```

Or run the harness directly:

```sh
benchmarks/run.sh --repeats 5
benchmarks/run.sh --case numeric_loop --repeats 10
benchmarks/run.sh --docker --repeats 3
benchmarks/run.sh --csv > results.csv
```

The harness is a small bash script - bash + standard Unix tools is the only
dependency on the host. `make bench-docker` no longer requires Python or PHP
installed locally because Python and PHP execute inside official CLI
containers.

The harness builds a local benchmark binary at `build/geblang-bench` unless
`GEBLANG_BIN` points at an existing executable. Python benchmarks use
`python3` from PATH (or `python:3.13-alpine` in Docker mode). PHP benchmarks
are skipped when `php` is not installed locally (in non-Docker mode); Docker
mode uses `php:8.4-cli-alpine` by default. Override container tags via
`BENCH_PYTHON_IMAGE` and `BENCH_PHP_IMAGE`. Docker mode pulls images
up-front before the timing loop so the first measurement isn't dominated
by a fresh layer download. Wall-clock timings in Docker mode still
include `docker run` startup overhead (typically 500-700ms per
invocation) - this is constant per workload, not a regression in the
benchmarks themselves.

The numbers are intended for local comparison and regression tracking. They are
not official cross-language rankings: CPU model, OS, Go version, PHP/Python
version, warm cache state, and benchmark shape all matter.
