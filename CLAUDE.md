# CLAUDE.md

## Project Overview

NATS Kubernetes Operator that manages NATS accounts and users using nkey-based authentication (no JWTs). Generates NATS server auth config, stores it in ConfigMaps.

## Build Commands

```bash
make generate      # Regenerate deepcopy and generated code
make manifests     # Regenerate CRD YAML and RBAC
make test          # Run all unit + envtest integration tests
make build         # Build operator binary
```

Always run `make generate && make manifests` after modifying CRD types in `api/v1alpha1/`.

## Go Environment

Go is managed via mise (`.mise.toml`). operator-sdk v1.42.2 is installed at `/usr/local/bin/operator-sdk`.

If `go mod tidy` fails with DNS timeouts, use `GOPROXY=direct go mod tidy`.

## Architecture

- **Domain:** `k8s.sandstorm.de`, API group: `nats.k8s.sandstorm.de`
- **3 CRDs:** NatsCluster, NatsAccount, NatsUser
- **Single controller** (`NatsClusterReconciler`) watches all 3 CRDs
- NatsAccount must be in same namespace as NatsCluster
- NatsUser can be cross-namespace (controlled by `allowedUserNamespaces` regex on NatsAccount)
- Config generation in `internal/natsconfig/` (pure Go, no K8s deps)
- NKey management in `internal/nkeys/`

## Conventions

- TDD: write tests first, then implement
- Plans stored in `docs/` as `YYYY_MM_DD_A_topicName.md`
- No `defaultPermissions` on accounts (security: permissions must be explicit per user)
- Safe-by-default bootstrap: when zero `NatsUser` resources exist across all accounts, `natsconfig.Generate` injects a deny-all sentinel user into `$G` so NATS rejects anonymous clients instead of silently routing them to `$G`. Sentinel nkey is a hardcoded throwaway constant for stability across reconciles.
- Own Go types for NATS config generation (not importing nats-server)
