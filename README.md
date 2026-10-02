# Startup Daemon

Startup Daemon starts containers when they are requested and stops them after
they have not been used for their configured TTL.

Containers are selected using the `startup.host` Docker label by default.

## Requirements

- Go 1.25 or newer
- Access to a Docker Engine

The Docker client uses the standard Docker environment variables, such as
`DOCKER_HOST`, `DOCKER_CONTEXT`, and `DOCKER_CERT_PATH`.

## Configuration

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `LISTEN_ADDRESS` | No | `:8080` | HTTP listen address |
| `POKE_TOKEN` | Yes | — | Bearer token required by the poke endpoint |
| `LABEL_PREFIX` | No | `startup` | Prefix used for container labels |

The lease table is currently held in memory. Restarting the daemon removes all
existing leases.

## Container labels

| Label | Required | Default | Description |
| --- | --- | --- | --- |
| `<prefix>.host` | Yes | — | Host value matched by `X-Forwarded-Host` |
| `<prefix>.ttl` | Yes | — | Go duration, for example `30m` or `2h` |

Example container:

```bash
docker run -d \
  --label startup.host=app.example.com \
  --label startup.ttl=30m \
  nginx
```

With the default prefix, the example uses `startup.host` and `startup.ttl`.
Set `LABEL_PREFIX` to use a different prefix.

Supported TTLs must be positive and no longer than 24 hours.

## Running

```bash
export POKE_TOKEN='change-me'
go run ./cmd
```

The daemon also requires Docker client access in the environment where it is
running.

## Traefik middleware

Configure Traefik's `forwardAuth` middleware to call Startup Daemon before the
request is sent to the application. The middleware must forward the original
host so Startup Daemon can find the matching container.

The following is a Traefik dynamic configuration file. Replace the token and
service names for your environment:

```yaml
http:
  middlewares:
    startup-auth-credentials:
      headers:
        customRequestHeaders:
          Authorization: "Bearer change-me"

    startup-auth:
      forwardAuth:
        address: "http://startup-daemon:8080/v1/poke"
        authRequestHeaders:
          - Authorization
          - X-Forwarded-Host
        trustForwardHeader: true

    startup-retry:
      retry:
        attempts: 10
        initialInterval: 500ms

    startup-auth-chain:
      chain:
        middlewares:
          - startup-auth-credentials
          - startup-auth
          - startup-retry

  routers:
    app:
      rule: "Host(`app.example.com`)"
      entryPoints:
        - web
      middlewares:
        - startup-auth-chain
      service: app

  services:
    app:
      loadBalancer:
        # Use a stable Docker DNS name, not a container IP.
        servers:
          - url: "http://app:80"
```

The `startup-retry` middleware is important. The first request starts the
application, so Traefik may briefly receive a connection-refused response
while the application is becoming ready.

If Traefik discovers the application through Docker labels instead of a file
provider, keep the router available for stopped containers and configure a
stable service URL:

```yaml
services:
  app:
    labels:
      traefik.enable: "true"
      traefik.docker.allownonrunning: "true"
      traefik.http.routers.app.rule: "Host(`app.example.com`)"
      traefik.http.routers.app.entrypoints: web
      traefik.http.routers.app.middlewares: startup-auth-chain
      traefik.http.services.app.loadbalancer.server.url: "http://app:80"
      startup.example.host: app.example.com
      startup.example.ttl: 30m
```

The `Authorization` value must match `POKE_TOKEN`. Keep this secret out of
public configuration and source control in production. Traefik and Startup
Daemon must share a Docker network, and the daemon must be able to access the
Docker Engine.

## API

### `POST /v1/poke`

Pokes all containers whose host label matches `X-Forwarded-Host`. Running
containers receive a new lease. Paused containers are unpaused, and created or
exited containers are started.

Example:

```bash
curl -X POST http://localhost:8080/v1/poke \
  -H 'Authorization: Bearer change-me' \
  -H 'X-Forwarded-Host: app.example.com'
```

Successful response:

```json
{"msg":"ok"}
```

The `X-Forwarded-Host` header must be supplied by a trusted proxy or otherwise
protected from spoofing. The bearer token must be kept secret.

## Development

Run tests and static checks with:

```bash
go test ./...
go vet ./...
```
