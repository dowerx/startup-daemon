# Compose test stack

Start the stack from this directory:

```bash
docker compose up --build -d
```

Test nginx through Traefik:

```bash
curl -H 'Host: nginx.localhost' \
  http://localhost:8080
```

Stop nginx and access it again. Traefik will call the daemon's forward-auth
endpoint, which starts nginx and refreshes its lease. The
`traefik.docker.allownonrunning=true` label keeps the router and middleware
available while nginx is stopped:

```bash
docker compose stop nginx

curl -H 'Host: nginx.localhost' \
  http://localhost:8080
```

The nginx container is stopped automatically after two minutes without
another poke.

Clean up:

```bash
docker compose down
```
