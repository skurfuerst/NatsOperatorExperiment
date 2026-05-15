/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package e2e

// natsServerManifests deploys a minimal NATS server (1 replica, no TLS) that
// mirrors the production volume layout from ../../../natsv2/setup/nats.yaml:
// the operator-rendered ConfigMap (lifecycle-cluster-nats-config) is mounted
// at /etc/nats-config/auth/auth.conf, and the NATS process loads it via
// `include` from a small base config.
//
// We deliberately use nats:2.10-alpine instead of nats:2.10 — the operator's
// PodReloader execs `kill -HUP 1` and `cat /etc/nats-config/auth/auth.conf`
// inside the container, both of which only work on an image that has a
// shell/coreutils. That requirement was the root cause of one of the bugs
// we hit in production; running the test against a scratch-based image
// would let it slip through again.
const natsServerManifests = `
apiVersion: v1
kind: ConfigMap
metadata:
  name: lifecycle-nats-base-config
  namespace: nats-auth-operator-system
data:
  nats.conf: |
    listen: 0.0.0.0:4222
    http: 0.0.0.0:8222
    server_name: $POD_NAME
    # nats-server resolves include paths relative to the parent config's
    # directory via path.Join, which does NOT honor a leading slash on the
    # included path. An absolute "include /etc/nats-config/auth/auth.conf"
    # ends up being looked up at /etc/nats/etc/nats-config/auth/auth.conf
    # and fails. Use a relative path from /etc/nats/.
    include ../nats-config/auth/auth.conf
---
apiVersion: v1
kind: Service
metadata:
  name: lifecycle-nats
  namespace: nats-auth-operator-system
spec:
  selector:
    app: lifecycle-nats
  ports:
    - name: client
      port: 4222
      targetPort: 4222
    - name: monitor
      port: 8222
      targetPort: 8222
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: lifecycle-nats
  namespace: nats-auth-operator-system
spec:
  serviceName: lifecycle-nats
  replicas: 1
  selector:
    matchLabels:
      app: lifecycle-nats
  template:
    metadata:
      labels:
        app: lifecycle-nats
    spec:
      # The operator namespace is labeled with the "restricted" Pod Security
      # Standard, so this pod must declare an explicit hardened context to
      # pass admission. Values mirror the operator's own deployment.
      securityContext:
        runAsNonRoot: true
        runAsUser: 1000
        runAsGroup: 1000
        fsGroup: 1000
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: nats
          image: nats:2.10-alpine
          args: ["-c", "/etc/nats/nats.conf"]
          ports:
            - containerPort: 4222
              name: client
            - containerPort: 8222
              name: monitor
          env:
            - name: POD_NAME
              valueFrom:
                fieldRef:
                  fieldPath: metadata.name
          volumeMounts:
            - name: base-config
              mountPath: /etc/nats
              readOnly: true
            - name: auth-config
              mountPath: /etc/nats-config/auth
              readOnly: true
          readinessProbe:
            httpGet:
              path: /healthz
              port: 8222
            initialDelaySeconds: 2
            periodSeconds: 2
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
            readOnlyRootFilesystem: true
      volumes:
        - name: base-config
          configMap:
            name: lifecycle-nats-base-config
            items:
              - key: nats.conf
                path: nats.conf
        - name: auth-config
          configMap:
            name: lifecycle-cluster-nats-config
            optional: true
            items:
              - key: auth.conf
                path: auth.conf
`

// bootstrapManifests creates the NatsCluster + NatsAccount the operator will
// reconcile. The cluster's serverRef points at the StatefulSet above, so
// SIGHUP reloads exercise the real path.
const bootstrapManifests = `
apiVersion: nats.k8s.sandstorm.de/v1alpha1
kind: NatsCluster
metadata:
  name: lifecycle-cluster
  namespace: nats-auth-operator-system
spec:
  serverRef:
    kind: StatefulSet
    name: lifecycle-nats
  monitoringPort: 8222
---
apiVersion: nats.k8s.sandstorm.de/v1alpha1
kind: NatsAccount
metadata:
  name: lifecycle-acct
  namespace: nats-auth-operator-system
spec:
  clusterRef:
    name: lifecycle-cluster
  userRules:
    - action: grant
      sameNamespace: true
`

// aliceUserManifest is the NatsUser whose lifecycle the test exercises:
// connect succeeds while it exists, fails once it's deleted.
const aliceUserManifest = `
apiVersion: nats.k8s.sandstorm.de/v1alpha1
kind: NatsUser
metadata:
  name: lifecycle-alice
  namespace: nats-auth-operator-system
spec:
  accountRef:
    name: lifecycle-acct
  permissions:
    publish:
      allow: ["events.>"]
    subscribe:
      allow: ["events.>"]
`
