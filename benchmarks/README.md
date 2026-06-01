# Geblang Benchmarks

This directory contains small, repeatable benchmarks for tracking Geblang
runtime performance against familiar scripting-language baselines.

Run all benchmarks:

```sh
make bench
```

Run PHP, Python, and Node.js comparisons inside small Docker CLI containers:

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
dependency on the host. `make bench-docker` does not require Python, PHP,
or Node installed locally because each runtime executes inside an official
CLI container.

The harness builds a local benchmark binary at `build/geblang-bench` unless
`GEBLANG_BIN` points at an existing executable. Python benchmarks use
`python3` from PATH (or `python:3.13-alpine` in Docker mode). PHP benchmarks
use `php` from PATH (or `php:8.4-cli-alpine` in Docker mode). Node benchmarks
use `node` from PATH (or `node:22-alpine` in Docker mode). When a runtime is
missing on the host the corresponding rows are reported as `skipped`.
Override container tags via `BENCH_PYTHON_IMAGE`, `BENCH_PHP_IMAGE`, and
`BENCH_NODE_IMAGE`. Docker mode pulls images up-front before the timing
loop so the first measurement isn't dominated by a fresh layer download.
Wall-clock timings in Docker mode still include `docker run` startup
overhead (typically 500-700ms per invocation) - this is constant per
workload, not a regression in the benchmarks themselves.

The numbers are intended for local comparison and regression tracking. They are
not official cross-language rankings: CPU model, OS, Go version, PHP/Python/
Node version, warm cache state, and benchmark shape all matter.
