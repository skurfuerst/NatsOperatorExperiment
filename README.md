# NatsOperatorExperiment

Kubernetes operator that manages NATS accounts and users using nkey-based authentication (no JWTs). Generates NATS server auth config declaratively via CRDs, with automatic NKey generation, config rendering, and ConfigMap output.

## CRDs

- **NatsCluster** — references a pre-provisioned NATS cluster. Grouping anchor for accounts.
- **NatsAccount** — a NATS account with JetStream limits, connection limits, and cross-namespace user controls.
- **NatsUser** — a NATS user with publish/subscribe permissions. NKey pair auto-generated and stored in a Secret.

## Quick Start

```bash
# Install CRDs
make install

# Run the operator locally
make run

# Apply sample resources
kubectl apply -f config/samples/
```

## Development

```bash
make generate      # Regenerate deepcopy and generated code
make manifests     # Regenerate CRD YAML and RBAC
make test          # Run all unit + envtest integration tests
make build         # Build operator binary
```

## Architecture

- **Single controller** (`NatsClusterReconciler`) watches all 3 CRDs
- NatsAccount must be in the same namespace as NatsCluster
- NatsUser can be cross-namespace (controlled by `allowedUserNamespaces` regex on NatsAccount)
- Config generation in `internal/natsconfig/` (pure Go, no K8s deps)
- NKey management in `internal/nkeys/`
- Generated config written to ConfigMap `{cluster-name}-nats-config`

## Future Work

- [ ] **Exports/Imports** — cross-account service and stream sharing
- [ ] **Subject Mappings** — account-level subject aliasing
- [ ] **Reload mechanism** — configurable pod exec / SIGHUP for NATS server reload
- [ ] **System account** — designating a system account on NatsCluster
- [ ] **AllowResponses** — request-reply response permissions on users
- [ ] **Account nkeys** — if JWT mode support is ever added
- [ ] **Webhook/admission validation** — validating CRDs before they're persisted
- [ ] **CLI tool** — standalone binary for config generation without K8s
