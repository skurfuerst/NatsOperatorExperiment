package natsconfig

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"

	natsv1alpha1 "github.com/sandstorm/NatsAuthOperator/api/v1alpha1"
)

// AccountWithUsers pairs a NatsAccount with its resolved NatsUsers.
type AccountWithUsers struct {
	Account natsv1alpha1.NatsAccount
	Users   []UserWithPublicKey
}

// UserWithPublicKey pairs a NatsUser with its resolved NKey public key and inbox prefix.
// InboxPrefix is the resolved prefix (from secret or spec); empty string means no inbox isolation.
type UserWithPublicKey struct {
	User        natsv1alpha1.NatsUser
	PublicKey   string
	InboxPrefix string
}

// ConvertToNatsConfig converts CRD resources to the internal config representation.
func ConvertToNatsConfig(accounts []AccountWithUsers) *NatsConfig {
	cfg := &NatsConfig{
		Accounts: make(map[string]AccountConfig, len(accounts)),
	}

	for _, awu := range accounts {
		acctCfg := AccountConfig{}

		if awu.Account.Spec.JetStream != nil {
			acctCfg.JetStream = convertJetStream(awu.Account.Spec.JetStream)
		}

		if awu.Account.Spec.Limits != nil {
			acctCfg.Limits = convertLimits(awu.Account.Spec.Limits)
		}

		for _, uwk := range awu.Users {
			userCfg := UserConfig{
				NKey: uwk.PublicKey,
			}
			if uwk.User.Spec.Permissions != nil || uwk.InboxPrefix != "" {
				userCfg.Permissions = convertPermissions(uwk.User.Spec.Permissions, uwk.InboxPrefix)
			}
			acctCfg.Users = append(acctCfg.Users, userCfg)
		}

		cfg.Accounts[awu.Account.Name] = acctCfg
	}

	return cfg
}

func convertJetStream(js *natsv1alpha1.AccountJetStream) *JetStreamConfig {
	c := &JetStreamConfig{}
	if js.MaxMemory != nil {
		c.MaxMemory = quantityToNATSSize(js.MaxMemory)
	}
	if js.MaxFile != nil {
		c.MaxFile = quantityToNATSSize(js.MaxFile)
	}
	c.MaxStreams = js.MaxStreams
	c.MaxConsumers = js.MaxConsumers
	c.MaxBytesRequired = js.MaxBytesRequired
	if js.MemoryMaxStreamBytes != nil {
		c.MemoryMaxStreamBytes = quantityToNATSSize(js.MemoryMaxStreamBytes)
	}
	if js.DiskMaxStreamBytes != nil {
		c.DiskMaxStreamBytes = quantityToNATSSize(js.DiskMaxStreamBytes)
	}
	c.MaxAckPending = js.MaxAckPending
	return c
}

func convertLimits(l *natsv1alpha1.AccountLimits) *LimitsConfig {
	c := &LimitsConfig{}
	c.MaxConnections = l.MaxConnections
	c.MaxSubscriptions = l.MaxSubscriptions
	if l.MaxPayload != nil {
		c.MaxPayload = quantityToNATSSize(l.MaxPayload)
	}
	c.MaxLeafnodes = l.MaxLeafnodes
	return c
}

func convertPermissions(p *natsv1alpha1.Permissions, inboxPrefix string) *PermissionsConfig {
	c := &PermissionsConfig{}
	if p != nil {
		if p.Publish != nil {
			c.Publish = &PermissionRuleConfig{
				Allow: p.Publish.Allow,
				Deny:  p.Publish.Deny,
			}
		}
		if p.Subscribe != nil {
			c.Subscribe = &PermissionRuleConfig{
				Allow: p.Subscribe.Allow,
				Deny:  p.Subscribe.Deny,
			}
		}
		if p.AllowResponses != nil {
			c.AllowResponses = &ResponsePermissionConfig{
				Enabled: p.AllowResponses.ShouldEmit(),
				MaxMsgs: p.AllowResponses.MaxMsgs,
				TTL:     p.AllowResponses.TTL,
			}
		}
	}
	if inboxPrefix != "" {
		c.Subscribe = injectInboxPrefix(c.Subscribe, inboxPrefix)
	}
	return c
}

// injectInboxPrefix merges inbox-isolation rules into a subscribe permission rule.
// It ensures "_INBOX.>" is denied and "<prefix>.>" is allowed, without duplicating
// entries that the user may have already specified explicitly.
func injectInboxPrefix(sub *PermissionRuleConfig, prefix string) *PermissionRuleConfig {
	if sub == nil {
		sub = &PermissionRuleConfig{}
	}
	inboxDeny := "_INBOX.>"
	inboxAllow := prefix + ".>"
	if !containsString(sub.Deny, inboxDeny) {
		sub.Deny = append(sub.Deny, inboxDeny)
	}
	if !containsString(sub.Allow, inboxAllow) {
		sub.Allow = append(sub.Allow, inboxAllow)
	}
	return sub
}

func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// quantityToNATSSize converts a Kubernetes resource.Quantity to a NATS-friendly size string.
// NATS understands suffixes like K, M, G, T (base-10) and KI, MI, GI, TI (base-2).
// Kubernetes uses Ki, Mi, Gi, Ti for binary and K, M, G, T for decimal.
func quantityToNATSSize(q *resource.Quantity) string {
	bytes := q.Value()

	// Try binary units first (more common in K8s)
	switch {
	case bytes >= 1<<40 && bytes%(1<<40) == 0:
		return fmt.Sprintf("%dTI", bytes/(1<<40))
	case bytes >= 1<<30 && bytes%(1<<30) == 0:
		return fmt.Sprintf("%dGI", bytes/(1<<30))
	case bytes >= 1<<20 && bytes%(1<<20) == 0:
		return fmt.Sprintf("%dMI", bytes/(1<<20))
	case bytes >= 1<<10 && bytes%(1<<10) == 0:
		return fmt.Sprintf("%dKI", bytes/(1<<10))
	default:
		return fmt.Sprintf("%d", bytes)
	}
}
