# Custom Inbox Prefixes for Request-Reply Isolation

## Problem

NATS uses an **inbox subject** as the reply-to address in request-reply patterns. By default,
the NATS client generates subjects of the form `_INBOX.<randomId>`. This creates a security
problem in multi-user accounts:

- If user A has `subscribe: allow: ["_INBOX.>"]`, they can listen on ALL inbox replies within
  their account—including replies meant for user B.
- Even without intentional snooping, an operator granting broad `_INBOX.>` subscribe access
  is inadvertently allowing cross-user reply interception.

## JWT vs nkey-based auth

### JWT / Operator mode

In JWT mode, the `nsc` tool supports a `resp_prefix` claim in the user JWT. The NATS server
reads this claim and automatically rewrites inbox subjects at the wire level. The client needs
to be configured with a matching prefix (`nats.InboxPrefix("_INBOX_myapp")`), and the server
enforces that clients using that credential can only use subjects under that prefix.

### nkey / config-file mode (this operator)

**There is no server-side `inbox_prefix` field in plain config files.** The NATS server does
not enforce a custom inbox prefix for nkey users defined in the `accounts {}` block. This was
confirmed by checking the NATS server authorization docs and source config examples.

The workaround is **permission-based inbox isolation**:

1. Deny the user from subscribing to `_INBOX.>` (the default inbox prefix)
2. Allow the user to subscribe to `<customPrefix>.>` (their exclusive inbox space)
3. Configure the NATS client to use the same custom prefix when connecting

The server will honour these subscribe permissions, meaning other users cannot receive messages
sent to `<customPrefix>.*` unless they are explicitly permitted. The "enforcement" that the user
actually uses their custom prefix comes from the client configuration—the server simply restricts
what subjects the user can subscribe to.

### Consequence

This approach is slightly weaker than JWT mode because:
- There is no server-side guarantee that a client with the given nkey *uses* the custom prefix.
  If a client does not configure `nats.InboxPrefix(...)`, it will attempt to subscribe to
  `_INBOX.*`, which will be denied—making request-reply fail for that client. This acts as
  an incentive to use the prefix, but it is enforced indirectly.
- There is no mechanism to prevent a client from publishing directly to another user's inbox
  subject unless additional publish deny rules are added.

For most use cases, this level of isolation is sufficient: reply interception is blocked by
the subscribe deny on `_INBOX.>`.

## Operator implementation

### CRD: `inboxPrefix` on `NatsUser`

```yaml
apiVersion: nats.k8s.sandstorm.de/v1alpha1
kind: NatsUser
metadata:
  name: myapp
  namespace: default
spec:
  accountRef:
    name: my-account
  inboxPrefix: "_INBOX_myapp"    # <-- new field
  permissions:
    publish:
      allow: ["myapp.>"]
    subscribe:
      allow: ["myapp.>"]
      # _INBOX.> deny and _INBOX_myapp.> allow are auto-injected by the operator
```

When `inboxPrefix` is set, the operator **automatically injects** into the user's subscribe
permissions:
- `deny: ["_INBOX.>"]` — prevents listening on default inbox subjects
- `allow: ["<inboxPrefix>.>"]` — permits listening on the custom inbox prefix

These are merged with any user-specified allow/deny rules; existing entries are not duplicated.

### Generated NATS config

```
accounts {
  my-account {
    users = [
      {
        nkey: UABC...
        permissions {
          publish {
            allow: ["myapp.>"]
          }
          subscribe {
            allow: ["myapp.>", "_INBOX_myapp.>"]
            deny: ["_INBOX.>"]
          }
        }
      }
    ]
  }
}
```

### Client-side requirement

The NATS client **must** be configured to use the same inbox prefix:

```go
// Go client — note: CustomInboxPrefix, NOT InboxPrefix; must NOT have trailing dot
nc, err := nats.Connect(natsURL,
    nats.Nkey(publicKey, signingCallback),
    nats.CustomInboxPrefix("_INBOX_myapp"),
)
```

```bash
# NATS CLI
nats --inbox-prefix "_INBOX_myapp" request myapp.hello "world"
```

Without this, the client's `nats.Request(...)` calls will subscribe to `_INBOX.*` subjects,
which will be denied by the server and cause request-reply to fail.

## Naming conventions

Choose inbox prefixes that:
- Are unique per user (e.g. include the user/app name)
- Do not overlap with other users' prefixes
- Use a consistent namespace (e.g. `_INBOX_<username>` or `_I_<username>`)

Example per-user prefixes for an account with users `alice` and `bob`:
- `_INBOX_alice` → alice subscribes to `_INBOX_alice.>`
- `_INBOX_bob`   → bob subscribes to `_INBOX_bob.>`

Neither can read the other's reply-to subjects.

## Security notes

- **Publish to other inboxes**: This design only prevents *subscribing* to `_INBOX.>`. A user
  with publish access to `_INBOX.>` could still publish (spoof) a reply to another user's
  outstanding request. If stronger guarantees are needed, add `publish: deny: ["_INBOX.>"]`
  plus `allow_responses` to restrict publish to only genuine reply-to subjects.
- **Combining with `allow_responses`**: Using `inboxPrefix` together with `allowResponses`
  is the strongest configuration: `allow_responses` limits the user to responding only to
  subjects received via a valid request, and `inboxPrefix` ensures replies cannot be intercepted.

## Future work

- In a future version of this operator, if NATS server adds nkey-mode support for server-side
  inbox prefix enforcement (similar to JWT `resp_prefix`), the operator should emit that config
  field instead of relying on permission rules.
- Consider adding a webhook validator that warns when `inboxPrefix` is set but the user's
  subscribe permissions don't actually permit the prefix subject (e.g. if the user overrides
  with an explicit allow list that doesn't include `<prefix>.>`).
