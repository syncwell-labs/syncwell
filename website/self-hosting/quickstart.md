# Self-hosting quickstart

## Docker

```bash
docker run --rm -p 8080:8080 \
  -e SYNCWELL_SECRET="$(openssl rand -hex 32)" \
  -e SYNCWELL_ADMIN_TOKEN="$(openssl rand -hex 32)" \
  -v "$PWD/data:/data" \
  ghcr.io/nil-vaghani/syncwell:latest \
  -data /data
```

The runtime image is distroless and runs as a non-root user. Mount `/data` as a
writable volume owned by uid/gid `65532` (the image's `nonroot` user).

## Compose

```bash
cp .env.example .env
# fill SYNCWELL_SECRET and SYNCWELL_ADMIN_TOKEN
docker compose up -d
```

## Binary or Go

```bash
go build -o bin/syncwell ./cmd/syncwell
./bin/syncwell -addr :8080 -data ./data \
  -secret "$SYNCWELL_SECRET" \
  -admin-token "$SYNCWELL_ADMIN_TOKEN" \
  -origins https://app.example.com
```

Released archives (Linux/macOS/Windows, amd64/arm64) include checksums and SPDX
SBOMs; see the repository's releases page.

## TLS

Terminate TLS at a proxy (recommended) or use the built-in listener:

```bash
syncwell -tls-cert /etc/letsencrypt/live/HOST/fullchain.pem \
         -tls-key  /etc/letsencrypt/live/HOST/privkey.pem
```

TLS 1.2+ and HTTP/2 are enabled. Certificate issuance/renewal stays with your
proxy or ACME client.

## Verify

```bash
curl -fsS http://localhost:8080/healthz
curl -fsS http://localhost:8080/readyz
curl -fsS -H "Authorization: Bearer $SYNCWELL_ADMIN_TOKEN" \
     http://localhost:8080/stats
```

Open `/admin`, enter the admin token, and confirm the room list loads. Then open
the [demos](/guide/getting-started#_1-run-the-server) in two windows.

## Platform templates

The repository includes templates for Fly.io, Render, Docker Compose, nginx,
Caddy, and systemd under `deploy/` plus `fly.toml`, `render.yaml`, and
`docker-compose.yml`. Start from the [configuration](/self-hosting/configuration)
reference before exposing a public host.
