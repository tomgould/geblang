# Geblang Extension Examples

These examples implement the Geblang `ext` IPC protocol in Python, PHP, Go,
and Node.js.

Run the managed Python example from the repository root with:

```sh
go run ./cmd/geblang examples/ext_python.gb
```

The `examples/geblang.yaml` manifest configures `python_example` for
`ext.load`. The Docker Compose file starts all four examples as pre-started TCP
extensions for `ext.connect` experiments.

```sh
docker compose -f examples/extensions/docker-compose.yml up --build -d
```

Each service can also be started individually:

```sh
docker compose -f examples/extensions/docker-compose.yml up -d python-ext-example
docker compose -f examples/extensions/docker-compose.yml up -d php-ext-example
docker compose -f examples/extensions/docker-compose.yml up -d go-ext-example
docker compose -f examples/extensions/docker-compose.yml up -d node-ext-example
```

Run the TCP conformance check for all four examples with:

```sh
examples/extensions/run-conformance.sh
```

The runner builds and starts the four services, waits for their TCP ports, runs
`examples/ext_tcp_examples.gb` through the Geblang CLI/VM path, and then stops
the Compose stack.
