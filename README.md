# Media Intake Service Shell

> **Phase: setup/service shell**

This repository currently provides only the executable service shell: configuration loading, HTTP server lifecycle, graceful shutdown, and a liveness endpoint. It establishes the development and container baseline for later implementation.

## Prerequisites

- Docker, when building or running the container image

## Run locally

```sh
go run ./cmd/media-intake
```

The service uses these environment variables:

| Variable | Default | Purpose |
| --- | --- | --- |
| `HTTP_ADDR` | `:8080` | TCP address for the HTTP server. |
| `SHUTDOWN_TIMEOUT` | `15s` | Maximum graceful-shutdown duration, in Go duration syntax. |

With the service running, check liveness:

```sh
curl http://localhost:8080/livez
```

The response is `{"status":"alive"}` with HTTP status 200.

## Development commands

```sh
# Format Go source
go fmt ./...

# Run tests
go test ./...

# Run static analysis
go vet ./...

# Build the service
go build -o bin/media-intake ./cmd/media-intake
```

## Container

Build the image from the repository root:

```sh
docker build -t media-intake:local .
```

Run it on port 8080:

```sh
docker run --rm -p 8080:8080 media-intake:local
```

Configuration can be overridden with Docker environment options, for example:

```sh
docker run --rm -p 8080:8080 \
  -e HTTP_ADDR=:8080 \
  -e SHUTDOWN_TIMEOUT=30s \
  media-intake:local
```

## Current layout

```text
cmd/media-intake/  Service entry point and lifecycle wiring
internal/config/   Environment configuration and validation
internal/server/   HTTP router and liveness endpoint
Dockerfile         Multi-stage production image
```

## Deferred scope

The following are intentionally deferred beyond this setup/service-shell phase:

- Database selection, schema, migrations, and persistence
- Media intake APIs and processing behavior
- Transactional outbox and downstream delivery
- Docker Compose and supporting local infrastructure
