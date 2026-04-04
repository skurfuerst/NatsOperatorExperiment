package natsconfig

// NatsConfig represents the full NATS server auth configuration.
type NatsConfig struct {
	Accounts map[string]AccountConfig
}

// AccountConfig represents a single NATS account's configuration.
type AccountConfig struct {
	JetStream *JetStreamConfig
	Limits    *LimitsConfig
	Users     []UserConfig
}

// JetStreamConfig represents JetStream limits for an account.
type JetStreamConfig struct {
	MaxMemory            string
	MaxFile              string
	MaxStreams           *int64
	MaxConsumers         *int64
	MaxBytesRequired     *bool
	MemoryMaxStreamBytes string
	DiskMaxStreamBytes   string
	MaxAckPending        *int64
}

// LimitsConfig represents connection/resource limits for an account.
type LimitsConfig struct {
	MaxConnections   *int64
	MaxSubscriptions *int64
	MaxPayload       string
	MaxLeafnodes     *int64
}

// UserConfig represents a single NATS user within an account.
type UserConfig struct {
	NKey        string
	Permissions *PermissionsConfig
}

// PermissionsConfig represents publish/subscribe permissions.
type PermissionsConfig struct {
	Publish        *PermissionRuleConfig
	Subscribe      *PermissionRuleConfig
	AllowResponses *ResponsePermissionConfig
}

// ResponsePermissionConfig represents NATS allow_responses configuration.
// Enabled must be true for allow_responses to be emitted; false suppresses it
// (e.g. when the CRD field is set to the boolean false).
type ResponsePermissionConfig struct {
	Enabled bool
	MaxMsgs *int
	TTL     *string
}

// PermissionRuleConfig represents allowed and denied subjects.
type PermissionRuleConfig struct {
	Allow []string
	Deny  []string
}
