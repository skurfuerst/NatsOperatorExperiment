# Deny-by-default for NatsUsers with no permissions

## Problem

A `NatsUser` declared as:

```yaml
spec:
  accountRef: { name: foo }
  insecureSharedInboxPrefix: true
```

(no `spec.permissions` block) currently produces a NATS user config with **no `permissions { }` block at all**. NATS server interprets the absence of a permissions block as **full access**: the user can publish and subscribe to anything in the account.

Source of the issue: `internal/natsconfig/convert.go:46`

```go
if uwk.User.Spec.Permissions != nil || uwk.InboxPrefix != "" {
    userCfg.Permissions = convertPermissions(uwk.User.Spec.Permissions, uwk.InboxPrefix)
}
```

When `Spec.Permissions == nil` *and* `InboxPrefix == ""` (which happens whenever `insecureSharedInboxPrefix: true` because the inbox-prefix resolver is skipped in `nkey_secret_manager.go`), no `permissions` is assigned and `generate.go:182` therefore omits the block.

This violates the project's "Safe-by-default" principle (see `CLAUDE.md`: the sentinel user injection for the no-users bootstrap case already encodes this principle for `$G`).

## Goal

A NatsUser that omits `spec.permissions` must result in **no publish and no subscribe rights** unless rights are explicitly granted. Adding `insecureSharedInboxPrefix: true` must not silently widen access.

The generated NATS config must be **explicit**: emit `publish { deny: [">"] }` and `subscribe { deny: [">"] }` rather than relying on NATS's implicit "no allow means deny" interpretation of an empty `permissions { }` block. This matches the sentinel-user pattern already used for the bootstrap deny-all case (`generate.go:77-79`) and makes the intent obvious to anyone reading the rendered server config.

## Approach (TDD — test first)

### Step 1: Failing tests

**Test A** — convert layer, `internal/natsconfig/convert_test.go`:

```go
func TestConvertUserWithNoPermissionsAndSharedInboxPrefixDeniesAllExplicitly(t *testing.T) {
    user := natsv1alpha1.NatsUser{
        ObjectMeta: metav1.ObjectMeta{Name: "no-perms"},
        Spec: natsv1alpha1.NatsUserSpec{
            AccountRef:                natsv1alpha1.NamespacedObjectReference{Name: "acct"},
            InsecureSharedInboxPrefix: true,
            // Permissions intentionally nil
        },
    }
    cfg := ConvertToNatsConfig([]AccountWithUsers{{
        Account: natsv1alpha1.NatsAccount{ObjectMeta: metav1.ObjectMeta{Name: "acct"}},
        Users:   []UserWithKey{{User: user, PublicKey: "Uxxx", InboxPrefix: ""}},
    }})

    uc := cfg.Accounts["acct"].Users[0]
    require.NotNil(t, uc.Permissions)
    require.NotNil(t, uc.Permissions.Publish)
    require.Equal(t, []string{">"}, uc.Permissions.Publish.Deny)
    require.NotNil(t, uc.Permissions.Subscribe)
    require.Equal(t, []string{">"}, uc.Permissions.Subscribe.Deny)
}
```

**Test B** — partial permissions still get the *missing direction* explicitly denied. E.g. a user with only `publish.allow` declared must still receive an explicit `subscribe { deny: [">"] }`:

```go
func TestConvertUserWithOnlyPublishGetsExplicitSubscribeDeny(t *testing.T) {
    user := natsv1alpha1.NatsUser{
        Spec: natsv1alpha1.NatsUserSpec{
            AccountRef:                natsv1alpha1.NamespacedObjectReference{Name: "acct"},
            InsecureSharedInboxPrefix: true,
            Permissions: &natsv1alpha1.Permissions{
                Publish: &natsv1alpha1.PermissionRule{Allow: []string{"events.>"}},
            },
        },
    }
    // ... convert and assert:
    // uc.Permissions.Publish.Allow == ["events.>"]
    // uc.Permissions.Subscribe.Deny == [">"]
}
```

**Test C** — rendering, `internal/natsconfig/generate_test.go`: assert the rendered NATS config string for the same input contains both `publish {\n      deny: [">"]` and `subscribe {\n      deny: [">"]`.

**Test D** — explicit allows are not contaminated by the default. A user with `publish.allow: ["foo.>"]` and `subscribe.allow: ["bar.>"]` must produce exactly those allows with **no auto-injected deny** on either direction (otherwise allows would be unreachable in NATS rule evaluation? — NATS evaluates allow first; allow + deny on same direction is a valid pattern, but we should not inject `deny: [">"]` when `allow` is non-empty for that direction).

### Step 2: Fix in `convert.go`

1. Always call `convertPermissions` (drop the gate at line 46):

   ```go
   userCfg := UserConfig{
       NKey:        uwk.PublicKey,
       Permissions: convertPermissions(uwk.User.Spec.Permissions, uwk.InboxPrefix),
   }
   ```

2. Inside `convertPermissions`, after the existing copy logic and after `injectInboxPrefix`, add an **explicit deny-all backfill** for each direction that has no `allow` rule:

   ```go
   denyAll := []string{">"}
   if c.Publish == nil || len(c.Publish.Allow) == 0 {
       if c.Publish == nil {
           c.Publish = &PermissionRuleConfig{}
       }
       if !containsString(c.Publish.Deny, ">") {
           c.Publish.Deny = append(c.Publish.Deny, denyAll...)
       }
   }
   if c.Subscribe == nil || len(c.Subscribe.Allow) == 0 {
       if c.Subscribe == nil {
           c.Subscribe = &PermissionRuleConfig{}
       }
       if !containsString(c.Subscribe.Deny, ">") {
           c.Subscribe.Deny = append(c.Subscribe.Deny, denyAll...)
       }
   }
   ```

   The "no allow → add explicit deny `>`" rule keeps inbox subscribe rules intact: `injectInboxPrefix` always seeds `subscribe.allow` with the per-user inbox prefix, so when an inbox prefix is present the subscribe direction has an allow and we do **not** add the broad deny.

### Step 3: Audit existing tests

Existing tests in `convert_test.go` / `generate_test.go` that construct users with only one direction (e.g. only `publish.allow`) will start seeing an injected `deny: [">"]` on the missing direction. Update their expectations. Sentinel deny-all (`generate.go:77-79`) is unaffected because it sets the same denies explicitly.

### Step 4: Verify no other call site relies on `Permissions == nil`

`generate.go:182` already gates on `user.Permissions != nil`; after the fix it will always be non-nil. Grep `Permissions ==` / `Permissions !=` in `internal/` to confirm nothing else branches on nil.

## Verification

1. `make test` — new tests fail before fix, pass after.
2. `go test ./internal/natsconfig/...` — full suite green (any tests with single-direction permissions adjusted in Step 3).
3. Manual inspection: rendered NATS server config for `insecureSharedInboxPrefix: true` + no permissions contains explicit `publish { deny: [">"] }` and `subscribe { deny: [">"] }`.
4. Envtest integration test (`internal/controller/natscluster_controller_test.go` style) that creates such a NatsUser and asserts the generated ConfigMap contents.

## Out of scope

- Reporting back a `Warning` event/condition on a NatsUser whose effective permissions are deny-all (could be a follow-up to make the footgun discoverable from `kubectl describe`).
- CRD-level validation forcing users to declare `permissions` explicitly — keeping `permissions` optional is fine once the default is safe.

## Critical files

- `internal/natsconfig/convert.go` (line 46)
- `internal/natsconfig/convert_test.go` (new test)
- `internal/natsconfig/generate_test.go` (new test)
