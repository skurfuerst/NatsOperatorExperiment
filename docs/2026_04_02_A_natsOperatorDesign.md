# NATS Kubernetes Operator - Implementation Plan

## Context

We're building a Kubernetes operator that manages NATS accounts and users using nkey-based authentication (no JWTs). The operator generates NATS server auth config, stores it in ConfigMaps, and signals pre-provisioned NATS clusters to reload.

**Problem:** Managing NATS multi-tenant auth config by hand is error-prone. This operator makes it declarative via CRDs, with automatic nkey generation, config rendering, and reload.

**Environment:** Go 1.24.7, Ubuntu 24.04, x86_64. Fresh repo (LICENSE + README only).

**Key decisions:**
- **operator-sdk** for scaffolding, **mise** for dev tools (including operator-sdk via `ubi` backend)
- **Domain:** `k8s.sandstorm.de`
- **3 CRDs:** NatsCluster, NatsAccount, NatsUser (no ReferenceGrant)
- **Single controller** watching all CRDs — everything feeds into one NATS config
- **JetStream + limits** mirrored exactly from NATS config into CRDs
- **Namespace model:** Accounts in same namespace as cluster; `allowedUserNamespaces` (regex list) on NatsAccount for cross-namespace users
- **TDD** with envtest, **CLI mode** included
- Accounts don't need nkey pairs (only users need nkeys in non-JWT mode)

---

## Phase 0: Dev Environment & Scaffolding

1. Install mise: `curl https://mise.jdx.dev/install.sh | sh`
2. Create `.mise.toml` with operator-sdk via ubi backend
3. Scaffold project, create CLAUDE.md, create `docs/` plan file

```toml
# .mise.toml
[tools]
go = "1.24"
"ubi:operator-framework/operator-sdk" = "v1.42.2"
```

```bash
mise install
operator-sdk init --domain k8s.sandstorm.de --repo github.com/sandstorm/NatsAuthOperator
operator-sdk create api --group nats --version v1alpha1 --kind NatsCluster --resource --controller
operator-sdk create api --group nats --version v1alpha1 --kind NatsAccount --resource  # no --controller
operator-sdk create api --group nats --version v1alpha1 --kind NatsUser --resource     # no --controller
```

Only NatsCluster gets `--controller`. NatsAccount and NatsUser get `--resource` only (types but no separate controller).

---

## CRD Design

### 1. NatsCluster (namespaced)

```go
type NatsClusterSpec struct {
    // No spec fields needed for now. The cluster is a grouping anchor.
    // Future: reload mechanism config, system account ref, etc.
}

type NatsClusterStatus struct {
    Conditions          []metav1.Condition `json:"conditions,omitempty"`
    AccountCount        int                `json:"accountCount,omitempty"`
    UserCount           int                `json:"userCount,omitempty"`
    LastConfigHash      string             `json:"lastConfigHash,omitempty"`
}
```

**Example:**
```yaml
apiVersion: nats.k8s.sandstorm.de/v1alpha1
kind: NatsCluster
metadata:
  name: main
  namespace: nats-system
```

### 2. NatsAccount (same namespace as NatsCluster)

Mirrors NATS account-level config exactly. Initial version: `jetstream`, `limits`. No `defaultPermissions` (security concern — permissions should always be explicit per user). Future: exports, imports, mappings.

```go
type NatsAccountSpec struct {
    // ClusterRef references the NatsCluster. Must be in the same namespace.
    ClusterRef LocalObjectReference `json:"clusterRef"`

    // AllowedUserNamespaces is a list of regex patterns specifying which
    // namespaces NatsUsers can reference this account from. Empty = same namespace only.
    AllowedUserNamespaces []string `json:"allowedUserNamespaces,omitempty"`

    // JetStream configures JetStream limits for this account.
    JetStream *AccountJetStream `json:"jetstream,omitempty"`

    // Limits configures connection and resource limits.
    Limits *AccountLimits `json:"limits,omitempty"`
}

type LocalObjectReference struct {
    Name string `json:"name"`
}

type AccountJetStream struct {
    MaxMemory            *resource.Quantity `json:"maxMemory,omitempty"`
    MaxFile              *resource.Quantity `json:"maxFile,omitempty"`
    MaxStreams            *int64            `json:"maxStreams,omitempty"`
    MaxConsumers          *int64            `json:"maxConsumers,omitempty"`
    MaxBytesRequired      *bool             `json:"maxBytesRequired,omitempty"`
    MemoryMaxStreamBytes  *resource.Quantity `json:"memoryMaxStreamBytes,omitempty"`
    DiskMaxStreamBytes    *resource.Quantity `json:"diskMaxStreamBytes,omitempty"`
    MaxAckPending         *int64            `json:"maxAckPending,omitempty"`
}

type AccountLimits struct {
    MaxConnections   *int64             `json:"maxConnections,omitempty"`
    MaxSubscriptions *int64             `json:"maxSubscriptions,omitempty"`
    MaxPayload       *resource.Quantity `json:"maxPayload,omitempty"`
    MaxLeafnodes     *int64             `json:"maxLeafnodes,omitempty"`
}

type NatsAccountStatus struct {
    Conditions []metav1.Condition `json:"conditions,omitempty"`
    UserCount  int                `json:"userCount,omitempty"`
}
```

### 3. NatsUser (can be cross-namespace)

```go
type NatsUserSpec struct {
    // AccountRef references the NatsAccount. Cross-namespace if allowed.
    AccountRef NamespacedObjectReference `json:"accountRef"`

    // Permissions defines publish/subscribe permissions for this user.
    Permissions *Permissions `json:"permissions,omitempty"`
}

type NamespacedObjectReference struct {
    Name      string `json:"name"`
    Namespace string `json:"namespace,omitempty"` // empty = same namespace
}

type Permissions struct {
    Publish   *PermissionRule `json:"publish,omitempty"`
    Subscribe *PermissionRule `json:"subscribe,omitempty"`
}

type PermissionRule struct {
    Allow []string `json:"allow,omitempty"`
    Deny  []string `json:"deny,omitempty"`
}

type NatsUserStatus struct {
    Conditions    []metav1.Condition `json:"conditions,omitempty"`
    NKeyPublicKey string             `json:"nkeyPublicKey,omitempty"`
    SecretRef     *SecretReference   `json:"secretRef,omitempty"`
}

type SecretReference struct {
    Name string `json:"name"`
}
```

**NKey Secret** (`{userName}-nats-nkey`):
- `nkey-seed`: User's seed/private key (starts with `SU`)
- `nkey-public`: User's public key (starts with `U`)

---

## Architecture

### Single Controller Design

One controller (`NatsClusterReconciler`) watches all 3 CRDs. NatsAccount/NatsUser changes trigger re-reconciliation of their parent NatsCluster.

```go
func (r *NatsClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&natsv1alpha1.NatsCluster{}).
        Watches(&natsv1alpha1.NatsAccount{},
            handler.EnqueueRequestsFromMapFunc(r.mapAccountToCluster)).
        Watches(&natsv1alpha1.NatsUser{},
            handler.EnqueueRequestsFromMapFunc(r.mapUserToCluster)).
        Owns(&corev1.ConfigMap{}).
        Complete(r)
}
```

### Reconcile Loop

1. Fetch NatsCluster
2. List NatsAccounts in same namespace where `clusterRef.name` matches
3. For each account, compile its `allowedUserNamespaces` regex patterns
4. For each account, list NatsUsers (across allowed namespaces) where `accountRef` matches
5. Validate each user's namespace against its account's regex; set condition on invalid users
6. Ensure NKey Secrets — generate nkey pair if missing, store in Secret in user's namespace
7. Generate NATS config via `internal/natsconfig.Generate()`
8. Hash config; if changed from `status.lastConfigHash`, create/update ConfigMap (`{cluster-name}-nats-config`)
9. Update status (account count, user count, config hash, conditions)

### Config Generation (`internal/natsconfig/`)

Pure Go package, no K8s dependencies. Structured builder (not templates) producing NATS conf format.

**Why own types (not importing nats-server)?** The `nats-server` package has no config serialization (parse-only, no marshal). Importing it would pull in the entire NATS runtime as a dependency. The deprecated nats-io/nats-operator also used its own types.

```conf
# Auto-generated by nats-operator - DO NOT EDIT
accounts {
  my-account {
    jetstream {
      max_mem: 512M
      max_file: 1G
      max_streams: 10
      max_consumers: 100
    }
    users = [
      {
        nkey: UABC123...
        permissions {
          publish { allow: ["events.>"] }
          subscribe { allow: ["responses.>"] }
        }
      }
    ]
  }
}
```

Internal types in `internal/natsconfig/types.go`, conversion from CRD types in `convert.go` (handles `resource.Quantity` → NATS size strings).

### NKey Management (`internal/nkeys/`)

Uses `github.com/nats-io/nkeys`. Generates user nkey pair, stores seed in Secret owned by the NatsUser (owner references for cleanup).

### CLI Mode (`cmd/cli/`)

Cobra-based CLI reusing `internal/natsconfig` package:
- `generate` — reads CRD YAML files, produces NATS config
- `validate` — checks CRD manifests for errors

---

## Implementation Phases (TDD)

### Phase 1: Scaffold & Types
- Install mise + operator-sdk, scaffold project
- Define all 3 CRD types with JetStream/limits/permissions
- `make generate && make manifests`
- Create CLAUDE.md, `docs/` plan
- Verify: `make build` passes

### Phase 2: Config Generation (pure unit tests)
- `internal/natsconfig/generate_test.go`: empty config, single account with JetStream, limits, users with nkeys + permissions, multiple accounts, full config
- `internal/natsconfig/generate.go`: implement builder
- `internal/natsconfig/convert_test.go`: CRD→internal type conversion, Quantity→NATS strings
- `internal/natsconfig/convert.go`: implement

### Phase 3: NKey Management (unit tests)
- `internal/nkeys/manager_test.go`: generates valid pair, public key starts with "U", seed reconstructs public key
- `internal/nkeys/manager.go`: implement

### Phase 4: Controller (envtest)
- `suite_test.go`: envtest setup
- Tests in order:
  - NatsCluster alone → empty ConfigMap
  - + NatsAccount → ConfigMap with account config (JetStream, limits)
  - + NatsUser (same ns) → NKey Secret created, config includes user
  - Cross-namespace NatsUser with matching regex → allowed
  - Cross-namespace NatsUser without matching regex → rejected (condition)
  - Update account limits → ConfigMap updated
  - Delete account → ConfigMap regenerated
- Implement reconcile loop, mappers

### Phase 5: CLI Tool
- `cmd/cli/generate` reads YAML, produces NATS config
- `cmd/cli/validate` checks manifests

### Phase 6: Polish
- Sample manifests in `config/samples/`
- README updates

---

## File Structure

```
.mise.toml
CLAUDE.md
docs/2026_04_02_A_natsOperatorDesign.md

api/v1alpha1/
  natscluster_types.go
  natsaccount_types.go
  natsuser_types.go
  groupversion_info.go       # scaffolded

internal/
  controller/
    natscluster_controller.go       # single reconciler
    natscluster_controller_test.go  # envtest tests
    suite_test.go
    mappers.go                      # event→reconcile mapping
  natsconfig/
    types.go        # internal config representation
    generate.go     # NATS config builder
    generate_test.go
    convert.go      # CRD types → internal types
    convert_test.go
  nkeys/
    manager.go
    manager_test.go

cmd/
  main.go           # operator entrypoint (scaffolded)
  cli/
    main.go         # CLI entrypoint
    root.go
    generate.go
    validate.go

config/
  crd/bases/        # generated
  rbac/             # generated
  samples/          # example CRs
```

---

## Verification

```bash
make generate      # regenerate deepcopy + generated code
make manifests     # regenerate CRD YAML + RBAC
make test          # all unit + envtest tests
make build         # verify compilation
```

Manual (after Phase 4):
```bash
make install       # install CRDs into kind cluster
make run           # run controller locally
kubectl apply -f config/samples/
kubectl get configmap {cluster}-nats-config -o yaml  # verify config
kubectl get secrets -l app.kubernetes.io/managed-by=nats-operator
kubectl get natsclusters -o yaml  # check status
```

---

## Future Work (TODO in README)

Features not in initial version, tracked in README:
- **Exports/Imports** — cross-account service and stream sharing
- **Subject Mappings** — account-level subject aliasing
- **Reload mechanism** — configurable pod exec / SIGHUP for NATS server reload
- **System account** — designating a system account on NatsCluster
- **AllowResponses** — request-reply response permissions on users
- **Account nkeys** — if JWT mode support is ever added
- **Webhook/admission validation** — validating CRDs before they're persisted
