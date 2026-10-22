# Self-hosting quickstart

## Docker

Until the GHCR image is published with a release, build it locally:

```bash
git clone https://github.com/Nil-Vaghani/syncwell && cd syncwell
make docker                                   # tags syncwell:latest
mkdir -p data && sudo chown 65532:65532 data  # the image runs as uid 65532
docker run --rm -p 8080:8080 \
  -e SYNCWELL_SECRET="$(openssl rand -hex 32)" \
  -e SYNCWELL_ADMIN_TOKEN="$(openssl rand -hex 32)" \
  -v "$PWD/data:/data" \
  syncwell:latest \
  -data /data
```

When releases are live the same command works with
`ghcr.io/nil-vaghani/syncwell:latest` instead of `syncwell:latest`.

The runtime image is distroless and runs as a non-root user. Mount `/data` as a
writable volume owned by uid/gid `65532` (the image's `nonroot` user).

## Compose

```bash
cp .env.example .env
# fill SYNCWELL_SECRET and SYNCWELL_ADMIN_TOKEN (openssl rand -hex 32)
docker compose up -d --build
docker compose run --rm syncwell -mint demo   # room token for the demo pages
# open http://localhost:8080/?room=demo&token=<token>
```

## Binary or Go

```bash
go build -o bin/syncwell ./cmd/syncwell
./bin/syncwell -addr :8080 -data ./data \
  -secret "$SYNCWELL_SECRET" \
  -admin-token "$SYNCWELL_ADMIN_TOKEN" \
  -origins https://app.example.com
```

Once a release is tagged, archives (Linux/macOS/Windows, amd64/arm64) attach to
the repository's releases page with checksums and SPDX SBOMs; until then build
from source as above.

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

The repository includes templates for Render, Docker Compose, nginx, Caddy,
and systemd under `deploy/` plus `render.yaml` and `docker-compose.yml`. Render
Free needs no credit card but sleeps when idle and keeps rooms in memory. Start
from the [configuration](/self-hosting/configuration) reference before exposing
a public host.
