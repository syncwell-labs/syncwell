# Kubernetes & Helm

Syncwell provides an official Helm chart located in `deploy/helm/syncwell` for deploying production-ready, auto-scaling real-time collaboration clusters on Kubernetes.

## Prerequisites

- Kubernetes 1.22+
- Helm 3.8+
- An ingress controller supporting WebSockets (e.g., `ingress-nginx` or `traefik`)
- (Optional) A Redis cluster or standalone Redis instance when deploying more than 1 replica for horizontal pub/sub relay.

## Quickstart

Clone the repository and install the chart into your Kubernetes cluster:

```bash
git clone https://github.com/syncwell-labs/syncwell.git
cd syncwell

# Generate secure tokens for client JWT verification and admin operations
SYNCWELL_SECRET="$(openssl rand -hex 32)"
SYNCWELL_ADMIN_TOKEN="$(openssl rand -hex 32)"

# Deploy with Helm
helm install syncwell ./deploy/helm/syncwell \
  --namespace syncwell --create-namespace \
  --set config.secret="${SYNCWELL_SECRET}" \
  --set config.adminToken="${SYNCWELL_ADMIN_TOKEN}"
```

Check the deployment status:

```bash
kubectl get pods -n syncwell
kubectl get svc -n syncwell
```

## Configuration Reference

Key values in `values.yaml`:

| Parameter | Description | Default |
| --- | --- | --- |
| `replicaCount` | Number of pod replicas | `2` |
| `image.repository` | Syncwell Docker image repository | `syncwell/syncwell` |
| `image.tag` | Docker image tag | `0.5.0` |
| `config.addr` | Server listen address | `:8080` |
| `config.secret` | HMAC secret for client access tokens | `""` (required) |
| `config.adminToken` | Secret bearer token for `/admin` and `/metrics` | `""` (required) |
| `config.maxRooms` | Max concurrent active rooms | `100000` |
| `config.idleRoomTTL` | Inactivity duration before evicting room from RAM | `10m` |
| `persistence.enabled` | Persistent volume claim for append-only transaction logs | `true` |
| `persistence.size` | Disk size for room storage | `10Gi` |
| `redis.enabled` | Enable Redis cluster relay for horizontal multi-replica sync | `false` |
| `redis.addr` | Redis master or sentinel address | `redis-master:6379` |
| `draining.enabled` | Enable preStop connection draining lifecycle hook | `true` |
| `draining.preStopSleepSeconds` | Seconds to delay SIGTERM to allow LB detachment | `15` |
| `ingress.enabled` | Expose Syncwell via Kubernetes Ingress | `false` |

## Zero-Downtime Rolling Deploys (Connection Draining)

WebSocket connections are long-lived stateful TCP streams. When terminating a Syncwell pod during rolling updates or autoscaling down:

1. Kubernetes removes the terminating pod from Service endpoints and Ingress routing.
2. The `preStop` lifecycle hook runs:
   ```yaml
   lifecycle:
     preStop:
       exec:
         command: ["/bin/sh", "-c", "sleep 15"]
   ```
   During this grace period, the pod continues serving existing WebSocket clients, while the ingress controller ceases routing new connections to it.
3. `/readyz` begins failing while `/healthz` stays alive until SIGTERM.
4. When `SIGTERM` is received, Syncwell flushes pending transaction logs to disk and gracefully closes active WebSockets with standard code `1001` (Going Away).
5. Front-end clients automatically reconnect to the newly spawned pod and synchronize delta state seamlessly.

## Ingress & WebSocket Configuration

To prevent reverse proxies from prematurely terminating idle WebSocket connections:

```yaml
ingress:
  enabled: true
  className: "nginx"
  annotations:
    nginx.ingress.kubernetes.io/proxy-read-timeout: "3600"
    nginx.ingress.kubernetes.io/proxy-send-timeout: "3600"
    nginx.ingress.kubernetes.io/websocket-services: "syncwell"
  hosts:
    - host: sync.example.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: syncwell-tls
      hosts:
        - sync.example.com
```

## Multi-Pod Clustering with Redis

For multi-replica deployments (`replicaCount > 1`), enable the Redis relay so clients connected to different pods can collaborate on the same room in real time:

```yaml
replicaCount: 3

redis:
  enabled: true
  addr: "redis-cluster.database.svc.cluster.local:6379"
  password: "your-redis-password"
  cluster: true
```

## Security & Hardening

By default, the Syncwell chart adheres to the Kubernetes **Restricted Pod Security Standard**:
- Runs as non-root user `10001:10001`.
- `allowPrivilegeEscalation: false`.
- Drops all Linux capabilities (`capabilities.drop: ["ALL"]`).
- Resource limits and requests are pre-configured to prevent CPU/memory exhaustion.
