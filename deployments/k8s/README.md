# Orbit on Kubernetes (single-region reference)

Minimal manifests for running Orbit on a local or cloud Kubernetes cluster.
This is an **earned stretch** deployment: parity with Compose is the goal, not
multi-region production hardening.

## Prerequisites

- Kubernetes 1.28+ (`kubectl` configured)
- Container images built locally or pushed to a registry your cluster can pull

## Build images

From the repository root:

```bash
docker build -f deployments/docker/Dockerfile --build-arg TARGET=orbitd -t orbit/orbitd:local .
docker build -f deployments/docker/Dockerfile --build-arg TARGET=gateway -t orbit/gateway:local .
docker build -f deployments/docker/Dockerfile --build-arg TARGET=orbit-migrate -t orbit/orbit-migrate:local .
```

For kind/minikube, load images after building:

```bash
kind load docker-image orbit/orbitd:local orbit/gateway:local orbit/orbit-migrate:local
```

## Deploy

```bash
kubectl apply -k deployments/k8s
kubectl -n orbit wait --for=condition=ready pod -l app.kubernetes.io/name=postgres --timeout=120s
kubectl -n orbit logs job/orbit-migrate
kubectl -n orbit wait --for=condition=ready pod -l app.kubernetes.io/name=orbitd --timeout=120s
```

`orbitd` is exposed on NodePort **30551**; gateway device traffic on **30552**.
Adjust `deployments/k8s/kustomization.yaml` image names when using a remote registry.

## Smoke test

Port-forward or use NodePort addresses, then:

```bash
go run ./cmd/orbitctl submit \
  -address 127.0.0.1:30551 \
  -producer demo -idempotency-key k8s-smoke-1 \
  -device edge-1 -priority 4 -payload collect-diagnostics -expires-after 1h
```

Run a client against the gateway NodePort separately, or use
`scripts/smoke-online.ps1` with `ORBIT_LISTEN_ADDRESS` / gateway env overrides.

## Observability

Prometheus in Compose scrapes host-published metrics ports. On Kubernetes, add
a `ServiceMonitor` or pod annotations when you install Prometheus — not included
in this minimal manifest set.

## Teardown

```bash
kubectl delete -k deployments/k8s
```
