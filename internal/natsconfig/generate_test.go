package natsconfig

import (
	"strings"
	"testing"
)

func TestGenerateEmptyConfig(t *testing.T) {
	cfg := &NatsConfig{
		Accounts: map[string]AccountConfig{},
	}
	result := Generate(cfg)
	if !strings.Contains(result, "Auto-generated") {
		t.Error("expected auto-generated header")
	}
	if !strings.Contains(result, "accounts {") {
		t.Error("expected accounts block")
	}
}

func TestGenerateNilAccounts(t *testing.T) {
	cfg := &NatsConfig{}
	result := Generate(cfg)
	if !strings.Contains(result, "accounts {") {
		t.Error("expected accounts block even with nil map")
	}
}

func TestGenerateSingleAccountWithJetStream(t *testing.T) {
	maxStreams := int64(10)
	maxConsumers := int64(100)
	cfg := &NatsConfig{
		Accounts: map[string]AccountConfig{
			"my-account": {
				JetStream: &JetStreamConfig{
					MaxMemory:    "512M",
					MaxFile:      "1G",
					MaxStreams:   &maxStreams,
					MaxConsumers: &maxConsumers,
				},
			},
		},
	}
	result := Generate(cfg)
	if !strings.Contains(result, "my-account") {
		t.Error("expected account name")
	}
	if !strings.Contains(result, "jetstream") {
		t.Error("expected jetstream block")
	}
	if !strings.Contains(result, "max_mem: 512M") {
		t.Error("expected max_mem: 512M")
	}
	if !strings.Contains(result, "max_file: 1G") {
		t.Error("expected max_file: 1G")
	}
	if !strings.Contains(result, "max_streams: 10") {
		t.Error("expected max_streams: 10")
	}
	if !strings.Contains(result, "max_consumers: 100") {
		t.Error("expected max_consumers: 100")
	}
}

func TestGenerateAccountWithAllJetStreamFields(t *testing.T) {
	maxStreams := int64(10)
	maxConsumers := int64(100)
	maxBytesRequired := true
	maxAckPending := int64(1000)
	cfg := &NatsConfig{
		Accounts: map[string]AccountConfig{
			"full-js": {
				JetStream: &JetStreamConfig{
					MaxMemory:            "512M",
					MaxFile:              "1G",
					MaxStreams:           &maxStreams,
					MaxConsumers:         &maxConsumers,
					MaxBytesRequired:     &maxBytesRequired,
					MemoryMaxStreamBytes: "100M",
					DiskMaxStreamBytes:   "500M",
					MaxAckPending:        &maxAckPending,
				},
			},
		},
	}
	result := Generate(cfg)
	if !strings.Contains(result, "max_bytes_required: true") {
		t.Error("expected max_bytes_required")
	}
	if !strings.Contains(result, "memory_max_stream_bytes: 100M") {
		t.Error("expected memory_max_stream_bytes")
	}
	if !strings.Contains(result, "disk_max_stream_bytes: 500M") {
		t.Error("expected disk_max_stream_bytes")
	}
	if !strings.Contains(result, "max_ack_pending: 1000") {
		t.Error("expected max_ack_pending")
	}
}

func TestGenerateAccountWithLimits(t *testing.T) {
	maxConn := int64(1000)
	maxSubs := int64(65536)
	maxLeaf := int64(10)
	cfg := &NatsConfig{
		Accounts: map[string]AccountConfig{
			"limited": {
				Limits: &LimitsConfig{
					MaxConnections:   &maxConn,
					MaxSubscriptions: &maxSubs,
					MaxPayload:       "1M",
					MaxLeafnodes:     &maxLeaf,
				},
			},
		},
	}
	result := Generate(cfg)
	if !strings.Contains(result, "max_connections: 1000") {
		t.Error("expected max_connections")
	}
	if !strings.Contains(result, "max_subscriptions: 65536") {
		t.Error("expected max_subscriptions")
	}
	if !strings.Contains(result, "max_payload: 1M") {
		t.Error("expected max_payload")
	}
	if !strings.Contains(result, "max_leafnodes: 10") {
		t.Error("expected max_leafnodes")
	}
}

func TestGenerateUserWithNKey(t *testing.T) {
	cfg := &NatsConfig{
		Accounts: map[string]AccountConfig{
			"app": {
				Users: []UserConfig{
					{NKey: "UABC123456789"},
				},
			},
		},
	}
	result := Generate(cfg)
	if !strings.Contains(result, "nkey: UABC123456789") {
		t.Error("expected nkey in output")
	}
	if !strings.Contains(result, "users") {
		t.Error("expected users block")
	}
}

func TestGenerateUserWithPermissions(t *testing.T) {
	cfg := &NatsConfig{
		Accounts: map[string]AccountConfig{
			"app": {
				Users: []UserConfig{
					{
						NKey: "UXYZ987654321",
						Permissions: &PermissionsConfig{
							Publish: &PermissionRuleConfig{
								Allow: []string{"events.>", "commands.>"},
								Deny:  []string{"secret.>"},
							},
							Subscribe: &PermissionRuleConfig{
								Allow: []string{"responses.>"},
							},
						},
					},
				},
			},
		},
	}
	result := Generate(cfg)
	if !strings.Contains(result, `"events.>"`) {
		t.Error("expected events.> in publish allow")
	}
	if !strings.Contains(result, `"commands.>"`) {
		t.Error("expected commands.> in publish allow")
	}
	if !strings.Contains(result, `"secret.>"`) {
		t.Error("expected secret.> in publish deny")
	}
	if !strings.Contains(result, `"responses.>"`) {
		t.Error("expected responses.> in subscribe allow")
	}
	if !strings.Contains(result, "publish") {
		t.Error("expected publish block")
	}
	if !strings.Contains(result, "subscribe") {
		t.Error("expected subscribe block")
	}
}

func TestGenerateMultipleAccounts(t *testing.T) {
	cfg := &NatsConfig{
		Accounts: map[string]AccountConfig{
			"account-a": {
				Users: []UserConfig{{NKey: "UA111"}},
			},
			"account-b": {
				Users: []UserConfig{{NKey: "UB222"}},
			},
		},
	}
	result := Generate(cfg)
	if !strings.Contains(result, "account-a") {
		t.Error("expected account-a")
	}
	if !strings.Contains(result, "account-b") {
		t.Error("expected account-b")
	}
	if !strings.Contains(result, "UA111") {
		t.Error("expected UA111")
	}
	if !strings.Contains(result, "UB222") {
		t.Error("expected UB222")
	}
}

func TestGenerateFullConfig(t *testing.T) {
	maxStreams := int64(5)
	maxConsumers := int64(50)
	maxConn := int64(100)
	cfg := &NatsConfig{
		Accounts: map[string]AccountConfig{
			"team-a": {
				JetStream: &JetStreamConfig{
					MaxMemory:    "256M",
					MaxFile:      "512M",
					MaxStreams:   &maxStreams,
					MaxConsumers: &maxConsumers,
				},
				Limits: &LimitsConfig{
					MaxConnections: &maxConn,
				},
				Users: []UserConfig{
					{
						NKey: "UUSER1",
						Permissions: &PermissionsConfig{
							Publish: &PermissionRuleConfig{
								Allow: []string{"team-a.>"},
							},
							Subscribe: &PermissionRuleConfig{
								Allow: []string{"team-a.>"},
							},
						},
					},
					{
						NKey: "UUSER2",
					},
				},
			},
		},
	}
	result := Generate(cfg)

	// Verify structure
	if !strings.Contains(result, "team-a") {
		t.Error("expected team-a account")
	}
	if !strings.Contains(result, "UUSER1") {
		t.Error("expected UUSER1")
	}
	if !strings.Contains(result, "UUSER2") {
		t.Error("expected UUSER2")
	}
	if !strings.Contains(result, "jetstream") {
		t.Error("expected jetstream block")
	}
	if strings.Count(result, "nkey:") != 2 {
		t.Errorf("expected 2 nkey entries, got %d", strings.Count(result, "nkey:"))
	}
}

func TestGenerateAccountNoJetStreamNoLimits(t *testing.T) {
	cfg := &NatsConfig{
		Accounts: map[string]AccountConfig{
			"bare": {
				Users: []UserConfig{{NKey: "UBARE1"}},
			},
		},
	}
	result := Generate(cfg)
	if strings.Contains(result, "jetstream") {
		t.Error("should not contain jetstream block for bare account")
	}
	if strings.Contains(result, "limits") {
		t.Error("should not contain limits block for bare account")
	}
	if !strings.Contains(result, "UBARE1") {
		t.Error("expected user nkey")
	}
}

func TestGenerateUserWithAllowResponsesBoolean(t *testing.T) {
	cfg := &NatsConfig{
		Accounts: map[string]AccountConfig{
			"svc": {
				Users: []UserConfig{
					{
						NKey: "USVC1",
						Permissions: &PermissionsConfig{
							Publish: &PermissionRuleConfig{
								Allow: []string{"requests.>"},
							},
							AllowResponses: &ResponsePermissionConfig{Enabled: true},
						},
					},
				},
			},
		},
	}
	result := Generate(cfg)
	if !strings.Contains(result, "allow_responses: true") {
		t.Error("expected allow_responses: true")
	}
}

func TestGenerateUserWithAllowResponsesStructured(t *testing.T) {
	maxMsgs := 1
	ttl := "5m"
	cfg := &NatsConfig{
		Accounts: map[string]AccountConfig{
			"svc": {
				Users: []UserConfig{
					{
						NKey: "USVC2",
						Permissions: &PermissionsConfig{
							AllowResponses: &ResponsePermissionConfig{
								Enabled: true,
								MaxMsgs: &maxMsgs,
								TTL:     &ttl,
							},
						},
					},
				},
			},
		},
	}
	result := Generate(cfg)
	if !strings.Contains(result, "allow_responses {") {
		t.Error("expected structured allow_responses block")
	}
	if !strings.Contains(result, "max: 1") {
		t.Error("expected max: 1")
	}
	if !strings.Contains(result, `ttl: "5m"`) {
		t.Error(`expected ttl: "5m"`)
	}
}

func TestGenerateUserWithAllowResponsesPartial(t *testing.T) {
	maxMsgs := 5
	cfg := &NatsConfig{
		Accounts: map[string]AccountConfig{
			"svc": {
				Users: []UserConfig{
					{
						NKey: "USVC3",
						Permissions: &PermissionsConfig{
							AllowResponses: &ResponsePermissionConfig{
								Enabled: true,
								MaxMsgs: &maxMsgs,
							},
						},
					},
				},
			},
		},
	}
	result := Generate(cfg)
	if !strings.Contains(result, "allow_responses {") {
		t.Error("expected structured allow_responses block")
	}
	if !strings.Contains(result, "max: 5") {
		t.Error("expected max: 5")
	}
	if strings.Contains(result, "ttl") {
		t.Error("should not contain ttl when not set")
	}
}

func TestGenerateUserWithAllowResponsesDisabled(t *testing.T) {
	cfg := &NatsConfig{
		Accounts: map[string]AccountConfig{
			"svc": {
				Users: []UserConfig{
					{
						NKey: "USVC4",
						Permissions: &PermissionsConfig{
							AllowResponses: &ResponsePermissionConfig{Enabled: false},
						},
					},
				},
			},
		},
	}
	result := Generate(cfg)
	if strings.Contains(result, "allow_responses") {
		t.Error("expected no allow_responses when Enabled is false")
	}
}

func TestGenerateDeterministicAccountOrder(t *testing.T) {
	cfg := &NatsConfig{
		Accounts: map[string]AccountConfig{
			"zebra":  {Users: []UserConfig{{NKey: "UZ1"}}},
			"alpha":  {Users: []UserConfig{{NKey: "UA1"}}},
			"middle": {Users: []UserConfig{{NKey: "UM1"}}},
		},
	}
	// Generate multiple times and verify order is consistent
	first := Generate(cfg)
	for i := 0; i < 5; i++ {
		result := Generate(cfg)
		if result != first {
			t.Error("config generation is not deterministic")
		}
	}
	// Verify alphabetical order
	alphaIdx := strings.Index(first, "alpha")
	middleIdx := strings.Index(first, "middle")
	zebraIdx := strings.Index(first, "zebra")
	if alphaIdx > middleIdx || middleIdx > zebraIdx {
		t.Error("accounts should be in alphabetical order")
	}
}
