# NATS Operator

Kubernetes operator that manages NATS accounts and users using **nkey-based authentication** (no JWTs). Declares auth configuration via CRDs, auto-generates NKey credentials, and writes a ready-to-use NATS auth config into a ConfigMap.

## Overview

This operator takes a declarative approach to NATS auth management:

1. You define **NatsCluster**, **NatsAccount**, and **NatsUser** resources in Kubernetes
2. The operator generates NKey keypairs for each user, stores them in Secrets
3. A complete NATS `auth.conf` is rendered into a ConfigMap, ready to be mounted by your NATS server

The operator does **not** deploy or manage NATS server pods. It only generates the auth configuration. You provision NATS separately (e.g., via Helm chart or StatefulSet) and mount the ConfigMap.

### Why nkeys instead of JWTs?

NKey-based auth uses a static config file (`auth.conf`) that NATS loads directly. This is simpler to operate than JWT/resolver-based auth: no account server, no resolver config, no token rotation. The tradeoff is that config changes require a NATS reload, which the operator handles automatically via the [reload mechanism](#reload-mechanism).

## Concepts

**NatsAccounts are tenants** -- think of them as isolated customers, teams, or environments. Each account gets its own subject namespace, JetStream limits, and connection quotas. Accounts cannot see each other's messages by default.

**NatsUsers are applications/services** within a tenant. Each microservice, worker, or API gateway that connects to NATS gets its own NatsUser with fine-grained publish/subscribe permissions. The NKey credentials (stored in a Secret) are mounted into the service's pod.

For example, a multi-tenant SaaS platform might look like:

```
NatsCluster "main" (namespace: nats)
  |
  +-- NatsAccount "customer-a"          <-- tenant: Customer A
  |     |
  |     +-- NatsUser "api-gateway"      <-- service: handles HTTP requests
  |     +-- NatsUser "order-processor"  <-- service: processes orders
  |     +-- NatsUser "notification-svc" <-- service: sends notifications
  |
  +-- NatsAccount "customer-b"          <-- tenant: Customer B
  |     |
  |     +-- NatsUser "api-gateway"      <-- same architecture, fully isolated
  |     +-- NatsUser "analytics-worker" <-- different services per tenant
  |
  +-- NatsAccount "internal"            <-- tenant: your own platform services
        |
        +-- NatsUser "billing-svc"      <-- internal service
        +-- NatsUser "monitoring"       <-- internal service
```

Each account is a hard isolation boundary -- `customer-a`'s services can only see subjects within their account. A user like `api-gateway` gets permissions scoped to exactly the subjects it needs (e.g., may publish to `orders.>` but not `admin.>`).

## Architecture

- **Single controller** (`NatsClusterReconciler`) watches all 3 CRDs
- **NatsAccount** must be in the **same namespace** as its NatsCluster
- **NatsUser** can be in **any namespace**, gated by `allowedUserNamespaces` regex on the account
- Config generation is deterministic (sorted by name) and idempotent
- NKey Secrets are created in the **user's namespace**, owned by the NatsUser resource

## CRDs

### NatsCluster

The cluster is the top-level grouping anchor. All accounts and their config roll up into a single ConfigMap named `{cluster-name}-nats-config`.

```yaml
apiVersion: nats.k8s.sandstorm.de/v1alpha1
kind: NatsCluster
metadata:
  name: main
  namespace: nats
spec:
  # Optional: reference a Deployment/StatefulSet for automatic reload
  serverRef:
    kind: StatefulSet
    name: nats
```

| Field | Type | Description |
|-------|------|-------------|
| `spec.serverRef` | `WorkloadReference` | Optional. References a Deployment or StatefulSet to annotate on config changes, triggering a rolling restart. |
| `status.accountCount` | `int` | Number of accounts linked to this cluster. |
| `status.userCount` | `int` | Total users across all accounts. |
| `status.lastConfigHash` | `string` | SHA256 of the last generated config. |

### NatsAccount

An account represents a **tenant** (e.g., a customer, team, or environment). It defines the tenant's JetStream limits, connection limits, and which Kubernetes namespaces may create users belonging to this tenant.

```yaml
apiVersion: nats.k8s.sandstorm.de/v1alpha1
kind: NatsAccount
metadata:
  name: orders
  namespace: nats
spec:
  clusterRef:
    name: main
  allowedUserNamespaces:
    - "^team-.*$"
  jetstream:
    maxMemory: "512Mi"
    maxFile: "1Gi"
    maxStreams: 10
    maxConsumers: 100
  limits:
    maxConnections: 500
    maxPayload: "1Mi"
```

| Field | Type | Description |
|-------|------|-------------|
| `spec.clusterRef.name` | `string` | **Required.** Name of the NatsCluster in the same namespace. |
| `spec.allowedUserNamespaces` | `[]string` | Regex patterns. Users in other namespaces must match at least one. Users in the same namespace as the account are always allowed. |
| `spec.jetstream.maxMemory` | `Quantity` | JetStream memory limit (e.g., `512Mi`). |
| `spec.jetstream.maxFile` | `Quantity` | JetStream file storage limit. |
| `spec.jetstream.maxStreams` | `int64` | Maximum number of streams. |
| `spec.jetstream.maxConsumers` | `int64` | Maximum number of consumers. |
| `spec.jetstream.maxBytesRequired` | `bool` | Require max_bytes on stream creation. |
| `spec.jetstream.memoryMaxStreamBytes` | `Quantity` | Per-stream memory limit. |
| `spec.jetstream.diskMaxStreamBytes` | `Quantity` | Per-stream disk limit. |
| `spec.jetstream.maxAckPending` | `int64` | Maximum pending acks. |
| `spec.limits.maxConnections` | `int64` | Maximum client connections. |
| `spec.limits.maxSubscriptions` | `int64` | Maximum subscriptions. |
| `spec.limits.maxPayload` | `Quantity` | Maximum message payload size. |
| `spec.limits.maxLeafnodes` | `int64` | Maximum leaf node connections. |

### NatsUser

A user represents a **specific application or service** within a tenant's account. Each service that connects to NATS gets its own NatsUser with dedicated NKey credentials (stored in a Secret) and fine-grained publish/subscribe permissions.

```yaml
apiVersion: nats.k8s.sandstorm.de/v1alpha1
kind: NatsUser
metadata:
  name: order-service
  namespace: nats
spec:
  accountRef:
    name: orders
  permissions:
    publish:
      allow: ["orders.>", "events.>"]
      deny: ["admin.>"]
    subscribe:
      allow: ["orders.replies.>"]
    allowResponses:
      maxMsgs: 1
      ttl: "5m"
```

| Field | Type | Description |
|-------|------|-------------|
| `spec.accountRef.name` | `string` | **Required.** Name of the NatsAccount. |
| `spec.accountRef.namespace` | `string` | Namespace of the account. Defaults to the user's namespace. |
| `spec.permissions.publish.allow` | `[]string` | Subjects the user may publish to. |
| `spec.permissions.publish.deny` | `[]string` | Subjects denied for publishing (overrides allow). |
| `spec.permissions.subscribe.allow` | `[]string` | Subjects the user may subscribe to. |
| `spec.permissions.subscribe.deny` | `[]string` | Subjects denied for subscribing. |
| `spec.permissions.allowResponses` | `ResponsePermission` | Enables request-reply response permissions. If set with no fields: `allow_responses: true`. If `maxMsgs`/`ttl` set: structured form. |
| `status.nkeyPublicKey` | `string` | The user's NKey public key (starts with `U`). |
| `status.secretRef.name` | `string` | Name of the Secret containing the NKey seed and public key. |

## Installation

```bash
# Install CRDs into the cluster
make install

# Deploy the operator
make deploy IMG=<your-registry>/nats-operator:latest

# Or run locally for development
make run
```

## Usage

### Basic Setup

```bash
# Apply sample resources
kubectl apply -f config/samples/
```

This creates a NatsCluster `main`, NatsAccount `app-account` with JetStream limits, and NatsUser `app-user` with publish/subscribe permissions.

After reconciliation, inspect the generated config:

```bash
kubectl get configmap main-nats-config -o jsonpath='{.data.auth\.conf}'
```

### Cross-Namespace Users

To allow users from other namespaces, set `allowedUserNamespaces` on the account:

```yaml
apiVersion: nats.k8s.sandstorm.de/v1alpha1
kind: NatsAccount
metadata:
  name: shared
  namespace: nats
spec:
  clusterRef:
    name: main
  allowedUserNamespaces:
    - "^team-.*$"      # any namespace starting with "team-"
    - "^staging$"      # exact match
```

Then create a user in a matching namespace:

```yaml
apiVersion: nats.k8s.sandstorm.de/v1alpha1
kind: NatsUser
metadata:
  name: worker
  namespace: team-backend    # matches ^team-.*$
spec:
  accountRef:
    name: shared
    namespace: nats          # explicit cross-namespace reference
```

The NKey Secret is created in `team-backend` (the user's namespace), while the auth config goes into the ConfigMap in `nats` (the cluster's namespace).

### Request-Reply with AllowResponses

For services that receive requests and need to publish replies:

```yaml
apiVersion: nats.k8s.sandstorm.de/v1alpha1
kind: NatsUser
metadata:
  name: api-service
spec:
  accountRef:
    name: app-account
  permissions:
    subscribe:
      allow: ["api.requests.>"]
    publish:
      allow: ["_INBOX.>"]
    allowResponses:
      maxMsgs: 1
      ttl: "30s"
```

Use `allowResponses: {}` for the simple boolean form (`allow_responses: true`), or specify `maxMsgs` and/or `ttl` for the structured form.

### Mounting the ConfigMap in NATS

Add the ConfigMap as a volume in your NATS StatefulSet/Deployment and include it from the main NATS config:

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: nats
spec:
  template:
    spec:
      containers:
        - name: nats
          image: nats:latest
          args: ["-c", "/etc/nats/nats.conf"]
          volumeMounts:
            - name: config
              mountPath: /etc/nats/nats.conf
              subPath: nats.conf
            - name: auth-config
              mountPath: /etc/nats/auth.conf
              subPath: auth.conf
      volumes:
        - name: config
          configMap:
            name: nats-config       # your main NATS config
        - name: auth-config
          configMap:
            name: main-nats-config  # generated by the operator
```

In your main `nats.conf`:

```
include ./auth.conf
```

## NKey Secrets

For each NatsUser, the operator creates a Secret named `{user-name}-nats-nkey` in the user's namespace:

```bash
kubectl get secret app-user-nats-nkey -o jsonpath='{.data.nkey-public}' | base64 -d
# UABC123...

kubectl get secret app-user-nats-nkey -o jsonpath='{.data.nkey-seed}' | base64 -d
# SUABC123...
```

| Key | Description |
|-----|-------------|
| `nkey-public` | Public key (starts with `U`). Embedded in the auth config. |
| `nkey-seed` | Private seed (starts with `SU`). Used by NATS clients to authenticate. |

The Secret is owned by the NatsUser resource and is garbage-collected when the user is deleted. Seeds are generated once and never regenerated on subsequent reconciliations.

To use the seed in a NATS client, mount the Secret and pass it via the `nkey` option in your client library.

## Reload Mechanism

When `spec.serverRef` is set on a NatsCluster, the operator annotates the referenced Deployment or StatefulSet's pod template with the config hash whenever the auth config changes. This triggers a rolling restart, causing NATS pods to remount the updated ConfigMap.

```yaml
apiVersion: nats.k8s.sandstorm.de/v1alpha1
kind: NatsCluster
metadata:
  name: main
spec:
  serverRef:
    kind: StatefulSet    # or Deployment
    name: nats
    namespace: nats      # optional, defaults to cluster's namespace
```

The annotation `nats.k8s.sandstorm.de/config-hash` is set on `spec.template.metadata.annotations`, so only config changes trigger restarts. No-op reconciliations leave the workload untouched.

## CLI Tool

A standalone CLI is available at `cmd/cli/` for offline config generation and validation without a Kubernetes cluster:

```bash
# Build
go build -o nats-operator-cli ./cmd/cli/

# Generate config from CRD YAML files
nats-operator-cli generate -f cluster.yaml -f account.yaml -f user.yaml

# Validate CRD files
nats-operator-cli validate -f account.yaml
```

## Development

```bash
make generate      # Regenerate deepcopy and generated code
make manifests     # Regenerate CRD YAML and RBAC
make test          # Run all unit + envtest integration tests
make build         # Build operator binary
```

Always run `make generate && make manifests` after modifying CRD types in `api/v1alpha1/`.

Go is managed via mise (`.mise.toml`). operator-sdk v1.42.2 is installed at `/usr/local/bin/operator-sdk`.

## Status Conditions

All three CRDs report status conditions:

| Condition | Status | Reason | Meaning |
|-----------|--------|--------|---------|
| `Ready` | `True` | `Reconciled` | Resource reconciled successfully. |
| `Ready` | `False` | `ReconcileError` | Error during reconciliation. |
| `Ready` | `False` | `InvalidRegex` | Account has invalid `allowedUserNamespaces` regex. |
| `Ready` | `False` | `NamespaceNotAllowed` | User's namespace doesn't match account's `allowedUserNamespaces`. |

## Future Work

- [ ] **Exports/Imports** -- cross-account service and stream sharing
- [ ] **Subject Mappings** -- account-level subject aliasing
- [ ] **System account** -- designating a system account on NatsCluster
- [ ] **Account nkeys** -- if JWT mode support is ever added
- [ ] **Webhook/admission validation** -- validating CRDs before they're persisted
