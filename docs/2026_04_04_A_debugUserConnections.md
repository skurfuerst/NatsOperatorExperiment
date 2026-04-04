# Concept: Debug User Connection Problems

## Context

The operator currently operates in a "fire and forget" model: it generates `auth.conf`, writes it to a ConfigMap, and sends SIGHUP to NATS pods. There is **zero runtime visibility** into whether users can actually connect. When a user reports "my app can't connect to NATS", the operator provides no help -- you'd have to manually exec into a NATS pod and query monitoring endpoints.

**Goal:** Provide an on-demand debugging tool for connection problems, without polling overhead. Ships as part of the operator image. Handles multi-server NATS clusters.

## Approach: Kubernetes-Aware CLI in the Operator Image

1. **Build a `nats-debug` CLI binary** that ships alongside `manager` in the operator container image
2. The CLI is **Kubernetes-aware**: reads NatsCluster CRs, discovers all NATS server pods via `serverRef`, queries each pod's monitoring endpoint, and aggregates results
3. **Static debug hint** in NatsUser/NatsAccount status tells users the exact command to run
4. User runs `kubectl exec` into the operator pod to invoke the tool

### Why this approach?
- **No polling overhead** -- operator stays lightweight, no periodic HTTP calls
- **On-demand** -- only queries NATS when actively debugging
- **Multi-server aware** -- discovers all pods, queries each, aggregates
- **Ships in operator image** -- no separate deployment, no sidecar, always available
- **Self-documenting** -- `kubectl describe natsuser` shows the debug command

## Design

### 1. New spec field: `NatsCluster.spec.monitoringPort`

The CLI needs to know which port to query on each NATS pod. Default to 8222 (NATS standard).

```go
// MonitoringPort is the HTTP monitoring port on the NATS server pods.
// Defaults to 8222. Used by the nats-debug CLI to query connection status.
// +optional
// +kubebuilder:default=8222
MonitoringPort *int32 `json:"monitoringPort,omitempty"`
```

### 2. CLI: `cmd/nats-debug/main.go`

A second binary in the operator image. Kubernetes-aware -- uses in-cluster config (or kubeconfig) and the operator's service account.

**Commands:**

```bash
# Check connections for a specific user (by nkey public key)
nats-debug user-connections --cluster mycluster --namespace default --nkey UABC123...

# Check all connections for an account  
nats-debug account-connections --cluster mycluster --namespace default --account myaccount

# Overview of all connections
nats-debug connections --cluster mycluster --namespace default
```

**How it works (user-connections):**
1. Read `NatsCluster` CR to get `serverRef` and `monitoringPort`
2. Get the Deployment/StatefulSet, extract pod selector
3. List all Running pods matching the selector
4. For each pod: HTTP GET `http://<pod-ip>:<monitoringPort>/connz`
5. Filter connections where `nkey` matches the given public key
6. Aggregate and display results, noting which server each connection is on

**Example output for `user-connections`:**
```
User: UABC123...
Status: CONNECTED (2 active connections across 2 servers)

  Connection #1 (server: nats-0):
    CID:            42
    Remote:         10.0.1.5:54321
    Connected:      2h30m ago
    RTT:            1.23ms
    Subscriptions:  3
    Msgs In/Out:    1,234 / 567

  Connection #2 (server: nats-1):
    CID:            87
    Remote:         10.0.2.10:43210
    Connected:      1h15m ago
    RTT:            2.05ms
    Subscriptions:  1
    Msgs In/Out:    89 / 23
```

**Example when NOT connected:**
```
User: UABC123...
Status: NOT CONNECTED (queried 3 servers, 0 connections found)

Troubleshooting hints:
  - Verify the client is using the correct nkey seed from Secret "my-service-user-nats-nkey"
  - Check client logs for connection errors
  - Verify network connectivity to the NATS server
```

**Example for `account-connections`:**
```
Account: myaccount
Active Connections: 5 (across 3 users, 3 servers)

  User UABC123... : 2 connections (nats-0, nats-1)
  User UDEF456... : 2 connections (nats-0, nats-2)
  User UGHI789... : 1 connection  (nats-1)
```

### 3. Reusable package: `internal/natsmonitor/`

HTTP client logic in a shared package (testable with `httptest.Server`).

```go
// client.go
type MonitorClient interface {
    GetAllConnections(ctx context.Context, baseURL string) (*ConnzResult, error)
}

// types.go -- only the /connz fields we need
type ConnzResult struct {
    ServerID       string           `json:"server_id"`
    NumConnections int              `json:"num_connections"`
    Connections    []ConnectionInfo `json:"connections"`
}

type ConnectionInfo struct {
    CID           uint64 `json:"cid"`
    IP            string `json:"ip"`
    Port          int    `json:"port"`
    Account       string `json:"account"`
    NKey          string `json:"nkey"`
    RTT           string `json:"rtt"`
    Subscriptions int    `json:"subscriptions"`
    InMsgs        int64  `json:"in_msgs"`
    OutMsgs       int64  `json:"out_msgs"`
    InBytes       int64  `json:"in_bytes"`
    OutBytes      int64  `json:"out_bytes"`
    Uptime        string `json:"uptime"`
    Start         string `json:"start"`
}
```

### 4. Static debug hint in CRD status

The operator sets a `DebugCommand` field during reconciliation. No NATS queries needed -- it's a static string built from known metadata.

**NatsUser status:**
```go
// DebugCommand is a command to check this user's NATS connections.
// Run it via kubectl exec in the operator pod.
// +optional
DebugCommand string `json:"debugCommand,omitempty"`
```

Value:
```
nats-debug user-connections --cluster mycluster --namespace default --nkey UABC123...
```

**NatsAccount status:**
```go
// DebugCommand is a command to check this account's NATS connections.
// +optional
DebugCommand string `json:"debugCommand,omitempty"`
```

Value:
```
nats-debug account-connections --cluster mycluster --namespace default --account myaccount
```

The operator auto-discovers its own Deployment name and namespace at startup via Kubernetes Downward API (`POD_NAME`, `POD_NAMESPACE` env vars) and the pod's ownerReference chain (Pod → ReplicaSet → Deployment). The debug command includes the full `kubectl exec` prefix so users can copy-paste it directly. If discovery fails (e.g. running locally), it falls back to the bare `nats-debug` command.

### 5. kubectl experience

```
$ kubectl describe natsuser my-service-user
...
Status:
  Conditions:
    Type    Status  Reason      Message
    Ready   True    Reconciled  User reconciled successfully
  Debug Command:  nats-debug user-connections --cluster mycluster --namespace default --nkey UABC123...
  NKey Public Key: UABC123...
```

User then runs:
```bash
kubectl exec -it deploy/natsoperator-controller-manager -n nats-system -- \
  nats-debug user-connections --cluster mycluster --namespace default --nkey UABC123...
```

### 6. Dockerfile changes

Build both binaries in the builder stage, copy both to the final image:

```dockerfile
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o manager cmd/main.go
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o nats-debug cmd/nats-debug/main.go

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /workspace/manager .
COPY --from=builder /workspace/nats-debug .
```

### 7. RBAC

The `nats-debug` CLI runs under the operator's service account, which already has permissions to read NatsCluster, list Deployments/StatefulSets, and list Pods. No RBAC changes needed.

## Implementation Steps

### Step 1: `internal/natsmonitor/` package (TDD)
- `types.go` -- Go structs for `/connz` JSON response
- `client_test.go` -- tests with `httptest.Server`: connected user, not connected, multiple connections, endpoint unreachable, multi-server aggregation
- `client.go` -- `MonitorClient` interface + `HTTPMonitorClient` (5s timeout)

### Step 2: API type changes
- `api/v1alpha1/natscluster_types.go` -- add `MonitoringPort` to spec
- `api/v1alpha1/natsuser_types.go` -- add `DebugCommand` to status
- `api/v1alpha1/natsaccount_types.go` -- add `DebugCommand` to status
- `make generate && make manifests`

### Step 3: Controller integration
- After setting user/account Ready conditions, compute `DebugCommand` string
- Uses cluster name, namespace, user's nkey public key / account name
- If `serverRef` is not set, omit debug command (can't discover pods)

### Step 4: `cmd/nats-debug/` CLI
- `main.go` -- flag-based CLI with subcommands (user-connections, account-connections, connections)
- K8s client setup: in-cluster config with fallback to kubeconfig
- Pod discovery: read NatsCluster CR -> serverRef -> list pods (reuse pattern from `reloadNatsPods`)
- Query each pod's `/connz`, aggregate, format output

### Step 5: Build & Dockerfile
- Update Dockerfile to build and include `nats-debug`
- Add `nats-debug` to Makefile build targets
- `make test` -- all existing + new tests pass

## Files to create/modify
- `internal/natsmonitor/types.go` -- **new**: connz response types
- `internal/natsmonitor/client.go` -- **new**: HTTP monitor client
- `internal/natsmonitor/client_test.go` -- **new**: tests
- `cmd/nats-debug/main.go` -- **new**: CLI entrypoint
- `api/v1alpha1/natscluster_types.go` -- add MonitoringPort
- `api/v1alpha1/natsuser_types.go` -- add DebugCommand to status
- `api/v1alpha1/natsaccount_types.go` -- add DebugCommand to status
- `internal/controller/natscluster_controller.go` -- set DebugCommand during reconcile
- `Dockerfile` -- build + copy nats-debug binary
- `Makefile` -- add nats-debug build target

## Existing code to reuse
- Pod discovery pattern from `reloadNatsPods` (`internal/controller/natscluster_controller.go:233-264`): reads serverRef, gets Deployment/StatefulSet, extracts pod selector, lists Running pods
- `WorkloadReference` type from `api/v1alpha1/natscluster_types.go` for serverRef

## What this intentionally avoids
- **No operator polling** -- zero runtime overhead when not debugging
- **No NATS client library** -- HTTP `/connz` only
- **No $SYS account** -- too complex for v1
- **No separate deployment** -- binary ships in operator image

## Verification
1. `make test` -- all unit + envtest tests pass
2. `make build` -- both manager and nats-debug build successfully
3. Manual: deploy operator, create NatsCluster with serverRef + monitoringPort, create NatsUser
4. `kubectl describe natsuser` shows debugCommand
5. `kubectl exec` into operator pod, run the debug command, see connection status
6. Test with multi-node NATS cluster: verify connections from all servers are aggregated
