package natsconfig

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	natsv1alpha1 "github.com/sandstorm/NatsAuthOperator/api/v1alpha1"
)

func TestQuantityToNATSSize(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"512Mi", "512MI"},
		{"1Gi", "1GI"},
		{"256Ki", "256KI"},
		{"2Ti", "2TI"},
		{"1000", "1000"},
		{"1024", "1KI"},
		{"1048576", "1MI"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			q := resource.MustParse(tt.input)
			result := quantityToNATSSize(&q)
			if result != tt.expected {
				t.Errorf("quantityToNATSSize(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestConvertToNatsConfigEmpty(t *testing.T) {
	cfg := ConvertToNatsConfig(nil)
	if len(cfg.Accounts) != 0 {
		t.Errorf("expected 0 accounts, got %d", len(cfg.Accounts))
	}
}

func TestConvertToNatsConfigWithJetStream(t *testing.T) {
	maxStreams := int64(10)
	maxConsumers := int64(100)
	mem := resource.MustParse("512Mi")
	file := resource.MustParse("1Gi")

	accounts := []AccountWithUsers{
		{
			Account: natsv1alpha1.NatsAccount{
				ObjectMeta: metav1.ObjectMeta{Name: "my-account"},
				Spec: natsv1alpha1.NatsAccountSpec{
					JetStream: &natsv1alpha1.AccountJetStream{
						MaxMemory:    &mem,
						MaxFile:      &file,
						MaxStreams:   &maxStreams,
						MaxConsumers: &maxConsumers,
					},
				},
			},
		},
	}

	cfg := ConvertToNatsConfig(accounts)
	acct, ok := cfg.Accounts["my-account"]
	if !ok {
		t.Fatal("expected my-account in config")
	}
	if acct.JetStream == nil {
		t.Fatal("expected JetStream config")
	}
	if acct.JetStream.MaxMemory != "512MI" {
		t.Errorf("expected 512MI, got %s", acct.JetStream.MaxMemory)
	}
	if acct.JetStream.MaxFile != "1GI" {
		t.Errorf("expected 1GI, got %s", acct.JetStream.MaxFile)
	}
	if *acct.JetStream.MaxStreams != 10 {
		t.Errorf("expected 10, got %d", *acct.JetStream.MaxStreams)
	}
	if *acct.JetStream.MaxConsumers != 100 {
		t.Errorf("expected 100, got %d", *acct.JetStream.MaxConsumers)
	}
}

func TestConvertToNatsConfigWithLimits(t *testing.T) {
	maxConn := int64(500)
	payload := resource.MustParse("1Mi")

	accounts := []AccountWithUsers{
		{
			Account: natsv1alpha1.NatsAccount{
				ObjectMeta: metav1.ObjectMeta{Name: "limited"},
				Spec: natsv1alpha1.NatsAccountSpec{
					Limits: &natsv1alpha1.AccountLimits{
						MaxConnections: &maxConn,
						MaxPayload:     &payload,
					},
				},
			},
		},
	}

	cfg := ConvertToNatsConfig(accounts)
	acct := cfg.Accounts["limited"]
	if acct.Limits == nil {
		t.Fatal("expected Limits config")
	}
	if *acct.Limits.MaxConnections != 500 {
		t.Errorf("expected 500, got %d", *acct.Limits.MaxConnections)
	}
	if acct.Limits.MaxPayload != "1MI" {
		t.Errorf("expected 1MI, got %s", acct.Limits.MaxPayload)
	}
}

func TestConvertToNatsConfigWithUsers(t *testing.T) {
	accounts := []AccountWithUsers{
		{
			Account: natsv1alpha1.NatsAccount{
				ObjectMeta: metav1.ObjectMeta{Name: "app"},
			},
			Users: []UserWithPublicKey{
				{
					User: natsv1alpha1.NatsUser{
						Spec: natsv1alpha1.NatsUserSpec{
							Permissions: &natsv1alpha1.Permissions{
								Publish: &natsv1alpha1.PermissionRule{
									Allow: []string{"events.>"},
									Deny:  []string{"admin.>"},
								},
								Subscribe: &natsv1alpha1.PermissionRule{
									Allow: []string{"responses.>"},
								},
							},
						},
					},
					PublicKey: "UUSER123",
				},
				{
					User:      natsv1alpha1.NatsUser{},
					PublicKey: "UUSER456",
				},
			},
		},
	}

	cfg := ConvertToNatsConfig(accounts)
	acct := cfg.Accounts["app"]
	if len(acct.Users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(acct.Users))
	}

	u1 := acct.Users[0]
	if u1.NKey != "UUSER123" {
		t.Errorf("expected UUSER123, got %s", u1.NKey)
	}
	if u1.Permissions == nil {
		t.Fatal("expected permissions on user 1")
	}
	if len(u1.Permissions.Publish.Allow) != 1 || u1.Permissions.Publish.Allow[0] != "events.>" {
		t.Error("expected publish allow events.>")
	}
	if len(u1.Permissions.Publish.Deny) != 1 || u1.Permissions.Publish.Deny[0] != "admin.>" {
		t.Error("expected publish deny admin.>")
	}

	u2 := acct.Users[1]
	if u2.NKey != "UUSER456" {
		t.Errorf("expected UUSER456, got %s", u2.NKey)
	}
	// Deny-by-default: a user with no spec.permissions still gets explicit
	// deny-all on both directions so NATS denies all access.
	if u2.Permissions == nil {
		t.Fatal("expected non-nil permissions (deny-all) on user 2")
	}
	if !containsString(u2.Permissions.Publish.Deny, ">") {
		t.Errorf("expected publish.deny to contain \">\", got %+v", u2.Permissions.Publish)
	}
	if !containsString(u2.Permissions.Subscribe.Deny, ">") {
		t.Errorf("expected subscribe.deny to contain \">\", got %+v", u2.Permissions.Subscribe)
	}
}

func TestConvertInboxPrefixInjectsSubscribeRules(t *testing.T) {
	accounts := []AccountWithUsers{
		{
			Account: natsv1alpha1.NatsAccount{
				ObjectMeta: metav1.ObjectMeta{Name: "app"},
			},
			Users: []UserWithPublicKey{
				{
					User:        natsv1alpha1.NatsUser{},
					PublicKey:   "UINBOX1",
					InboxPrefix: "_INBOX_myapp",
				},
			},
		},
	}

	cfg := ConvertToNatsConfig(accounts)
	user := cfg.Accounts["app"].Users[0]
	if user.Permissions == nil {
		t.Fatal("expected permissions to be set")
	}
	sub := user.Permissions.Subscribe
	if sub == nil {
		t.Fatal("expected subscribe permissions to be set")
	}
	if !containsString(sub.Deny, "_INBOX.>") {
		t.Errorf("expected _INBOX.> in subscribe deny, got: %v", sub.Deny)
	}
	if !containsString(sub.Allow, "_INBOX_myapp.>") {
		t.Errorf("expected _INBOX_myapp.> in subscribe allow, got: %v", sub.Allow)
	}
}

func TestConvertInboxPrefixMergesWithExistingSubscribePermissions(t *testing.T) {
	accounts := []AccountWithUsers{
		{
			Account: natsv1alpha1.NatsAccount{
				ObjectMeta: metav1.ObjectMeta{Name: "app"},
			},
			Users: []UserWithPublicKey{
				{
					User: natsv1alpha1.NatsUser{
						Spec: natsv1alpha1.NatsUserSpec{
							Permissions: &natsv1alpha1.Permissions{
								Subscribe: &natsv1alpha1.PermissionRule{
									Allow: []string{"events.>"},
									Deny:  []string{"admin.>"},
								},
							},
						},
					},
					PublicKey:   "UALICE1",
					InboxPrefix: "_INBOX_alice",
				},
			},
		},
	}

	cfg := ConvertToNatsConfig(accounts)
	sub := cfg.Accounts["app"].Users[0].Permissions.Subscribe

	// Should contain the existing user-specified entries
	if !containsString(sub.Allow, "events.>") {
		t.Error("expected existing events.> in subscribe allow")
	}
	if !containsString(sub.Deny, "admin.>") {
		t.Error("expected existing admin.> in subscribe deny")
	}
	// Plus the auto-injected inbox entries
	if !containsString(sub.Allow, "_INBOX_alice.>") {
		t.Errorf("expected _INBOX_alice.> in subscribe allow, got: %v", sub.Allow)
	}
	if !containsString(sub.Deny, "_INBOX.>") {
		t.Errorf("expected _INBOX.> in subscribe deny, got: %v", sub.Deny)
	}
}

func TestConvertInboxPrefixNoDuplicateIfAlreadySet(t *testing.T) {
	accounts := []AccountWithUsers{
		{
			Account: natsv1alpha1.NatsAccount{
				ObjectMeta: metav1.ObjectMeta{Name: "app"},
			},
			Users: []UserWithPublicKey{
				{
					User: natsv1alpha1.NatsUser{
						Spec: natsv1alpha1.NatsUserSpec{
							Permissions: &natsv1alpha1.Permissions{
								Subscribe: &natsv1alpha1.PermissionRule{
									Allow: []string{"_INBOX_bob.>"},
									Deny:  []string{"_INBOX.>"},
								},
							},
						},
					},
					PublicKey:   "UBOB1",
					InboxPrefix: "_INBOX_bob",
				},
			},
		},
	}

	cfg := ConvertToNatsConfig(accounts)
	sub := cfg.Accounts["app"].Users[0].Permissions.Subscribe

	allowCount := 0
	for _, v := range sub.Allow {
		if v == "_INBOX_bob.>" {
			allowCount++
		}
	}
	if allowCount != 1 {
		t.Errorf("expected exactly 1 _INBOX_bob.> in allow, got %d", allowCount)
	}

	denyCount := 0
	for _, v := range sub.Deny {
		if v == "_INBOX.>" {
			denyCount++
		}
	}
	if denyCount != 1 {
		t.Errorf("expected exactly 1 _INBOX.> in deny, got %d", denyCount)
	}
}

// TestConvertUserWithNoPermissionsAndSharedInboxPrefixDeniesAllExplicitly guards
// against a footgun: a NatsUser that omits spec.permissions and opts into
// insecureSharedInboxPrefix (so no operator-generated InboxPrefix) must NOT
// receive full account access. Instead, the converter must emit an explicit
// deny-all on both publish and subscribe.
func TestConvertUserWithNoPermissionsAndSharedInboxPrefixDeniesAllExplicitly(t *testing.T) {
	accounts := []AccountWithUsers{
		{
			Account: natsv1alpha1.NatsAccount{ObjectMeta: metav1.ObjectMeta{Name: "acct"}},
			Users: []UserWithPublicKey{
				{
					User: natsv1alpha1.NatsUser{
						Spec: natsv1alpha1.NatsUserSpec{
							InsecureSharedInboxPrefix: true,
							// Permissions intentionally nil
						},
					},
					PublicKey:   "UNOPERMS",
					InboxPrefix: "",
				},
			},
		},
	}

	cfg := ConvertToNatsConfig(accounts)
	uc := cfg.Accounts["acct"].Users[0]
	if uc.Permissions == nil {
		t.Fatal("expected non-nil Permissions even without spec.permissions")
	}
	if uc.Permissions.Publish == nil || !containsString(uc.Permissions.Publish.Deny, ">") {
		t.Errorf("expected publish.deny to contain \">\", got %+v", uc.Permissions.Publish)
	}
	if uc.Permissions.Subscribe == nil || !containsString(uc.Permissions.Subscribe.Deny, ">") {
		t.Errorf("expected subscribe.deny to contain \">\", got %+v", uc.Permissions.Subscribe)
	}
}

// TestConvertUserWithOnlyPublishGetsExplicitSubscribeDeny verifies that when
// a user declares only one direction (publish), the other direction (subscribe)
// is filled in with an explicit deny-all.
func TestConvertUserWithOnlyPublishGetsExplicitSubscribeDeny(t *testing.T) {
	accounts := []AccountWithUsers{
		{
			Account: natsv1alpha1.NatsAccount{ObjectMeta: metav1.ObjectMeta{Name: "acct"}},
			Users: []UserWithPublicKey{
				{
					User: natsv1alpha1.NatsUser{
						Spec: natsv1alpha1.NatsUserSpec{
							InsecureSharedInboxPrefix: true,
							Permissions: &natsv1alpha1.Permissions{
								Publish: &natsv1alpha1.PermissionRule{Allow: []string{"events.>"}},
							},
						},
					},
					PublicKey: "UPUBONLY",
				},
			},
		},
	}

	cfg := ConvertToNatsConfig(accounts)
	uc := cfg.Accounts["acct"].Users[0]
	if uc.Permissions == nil {
		t.Fatal("expected non-nil Permissions")
	}
	if !containsString(uc.Permissions.Publish.Allow, "events.>") {
		t.Error("expected publish.allow to keep events.>")
	}
	if uc.Permissions.Subscribe == nil || !containsString(uc.Permissions.Subscribe.Deny, ">") {
		t.Errorf("expected subscribe.deny to contain \">\", got %+v", uc.Permissions.Subscribe)
	}
}

// TestConvertUserWithExplicitAllowsNotContaminatedByDefaultDeny ensures that
// when both publish and subscribe have allow rules, no broad ">" deny is
// auto-injected on either direction.
func TestConvertUserWithExplicitAllowsNotContaminatedByDefaultDeny(t *testing.T) {
	accounts := []AccountWithUsers{
		{
			Account: natsv1alpha1.NatsAccount{ObjectMeta: metav1.ObjectMeta{Name: "acct"}},
			Users: []UserWithPublicKey{
				{
					User: natsv1alpha1.NatsUser{
						Spec: natsv1alpha1.NatsUserSpec{
							InsecureSharedInboxPrefix: true,
							Permissions: &natsv1alpha1.Permissions{
								Publish:   &natsv1alpha1.PermissionRule{Allow: []string{"foo.>"}},
								Subscribe: &natsv1alpha1.PermissionRule{Allow: []string{"bar.>"}},
							},
						},
					},
					PublicKey: "UALLOWS",
				},
			},
		},
	}

	cfg := ConvertToNatsConfig(accounts)
	uc := cfg.Accounts["acct"].Users[0]
	if containsString(uc.Permissions.Publish.Deny, ">") {
		t.Errorf("publish.deny must not contain broad \">\", got %+v", uc.Permissions.Publish.Deny)
	}
	if containsString(uc.Permissions.Subscribe.Deny, ">") {
		t.Errorf("subscribe.deny must not contain broad \">\", got %+v", uc.Permissions.Subscribe.Deny)
	}
}

// TestConvertUserWithInboxPrefixDoesNotInjectBroadSubscribeDeny ensures that
// when the operator allocates an inbox prefix (insecureSharedInboxPrefix=false),
// subscribe gets the per-user inbox allow + _INBOX.> deny, but NOT a broad ">" deny.
func TestConvertUserWithInboxPrefixDoesNotInjectBroadSubscribeDeny(t *testing.T) {
	accounts := []AccountWithUsers{
		{
			Account: natsv1alpha1.NatsAccount{ObjectMeta: metav1.ObjectMeta{Name: "acct"}},
			Users: []UserWithPublicKey{
				{
					User:        natsv1alpha1.NatsUser{},
					PublicKey:   "UINBOX",
					InboxPrefix: "_I_abc",
				},
			},
		},
	}

	cfg := ConvertToNatsConfig(accounts)
	uc := cfg.Accounts["acct"].Users[0]
	if uc.Permissions == nil || uc.Permissions.Subscribe == nil {
		t.Fatal("expected subscribe permissions from inbox prefix injection")
	}
	if !containsString(uc.Permissions.Subscribe.Allow, "_I_abc.>") {
		t.Error("expected subscribe.allow to contain per-user inbox prefix")
	}
	if containsString(uc.Permissions.Subscribe.Deny, ">") {
		t.Errorf("subscribe.deny must not contain broad \">\" when inbox allow is present, got %+v", uc.Permissions.Subscribe.Deny)
	}
	// Publish still has no allow, so it must be deny-all.
	if uc.Permissions.Publish == nil || !containsString(uc.Permissions.Publish.Deny, ">") {
		t.Errorf("expected publish.deny to contain \">\" when no publish.allow set, got %+v", uc.Permissions.Publish)
	}
}

func TestConvertToNatsConfigWithAllowResponses(t *testing.T) {
	maxMsgs := 1
	ttl := "5m"

	accounts := []AccountWithUsers{
		{
			Account: natsv1alpha1.NatsAccount{
				ObjectMeta: metav1.ObjectMeta{Name: "svc"},
			},
			Users: []UserWithPublicKey{
				{
					// User 1: structured object form with maxMsgs + ttl
					User: natsv1alpha1.NatsUser{
						Spec: natsv1alpha1.NatsUserSpec{
							Permissions: &natsv1alpha1.Permissions{
								Publish: &natsv1alpha1.PermissionRule{
									Allow: []string{"requests.>"},
								},
								AllowResponses: &natsv1alpha1.AllowResponsesSpec{
									MaxMsgs: &maxMsgs,
									TTL:     &ttl,
								},
							},
						},
					},
					PublicKey: "USVC1",
				},
				{
					// User 2: empty object form {} → allow_responses: true
					User: natsv1alpha1.NatsUser{
						Spec: natsv1alpha1.NatsUserSpec{
							Permissions: &natsv1alpha1.Permissions{
								AllowResponses: &natsv1alpha1.AllowResponsesSpec{},
							},
						},
					},
					PublicKey: "USVC2",
				},
				{
					// User 3: boolean true form
					User: natsv1alpha1.NatsUser{
						Spec: natsv1alpha1.NatsUserSpec{
							Permissions: &natsv1alpha1.Permissions{
								AllowResponses: natsv1alpha1.NewAllowResponsesBool(true),
							},
						},
					},
					PublicKey: "USVC3",
				},
				{
					// User 4: boolean false form → do not emit
					User: natsv1alpha1.NatsUser{
						Spec: natsv1alpha1.NatsUserSpec{
							Permissions: &natsv1alpha1.Permissions{
								AllowResponses: natsv1alpha1.NewAllowResponsesBool(false),
							},
						},
					},
					PublicKey: "USVC4",
				},
			},
		},
	}

	cfg := ConvertToNatsConfig(accounts)
	acct := cfg.Accounts["svc"]

	// User 1: structured allow_responses
	u1 := acct.Users[0]
	if u1.Permissions.AllowResponses == nil {
		t.Fatal("expected AllowResponses on user 1")
	}
	if !u1.Permissions.AllowResponses.Enabled {
		t.Error("expected Enabled=true for user 1")
	}
	if *u1.Permissions.AllowResponses.MaxMsgs != 1 {
		t.Errorf("expected MaxMsgs 1, got %d", *u1.Permissions.AllowResponses.MaxMsgs)
	}
	if *u1.Permissions.AllowResponses.TTL != "5m" {
		t.Errorf("expected TTL 5m, got %s", *u1.Permissions.AllowResponses.TTL)
	}

	// User 2: empty object form → Enabled=true, no MaxMsgs/TTL
	u2 := acct.Users[1]
	if u2.Permissions.AllowResponses == nil {
		t.Fatal("expected AllowResponses on user 2")
	}
	if !u2.Permissions.AllowResponses.Enabled {
		t.Error("expected Enabled=true for empty object form")
	}
	if u2.Permissions.AllowResponses.MaxMsgs != nil {
		t.Error("expected nil MaxMsgs for boolean form")
	}
	if u2.Permissions.AllowResponses.TTL != nil {
		t.Error("expected nil TTL for boolean form")
	}

	// User 3: boolean true → Enabled=true
	u3 := acct.Users[2]
	if u3.Permissions.AllowResponses == nil {
		t.Fatal("expected AllowResponses on user 3")
	}
	if !u3.Permissions.AllowResponses.Enabled {
		t.Error("expected Enabled=true for boolean true form")
	}

	// User 4: boolean false → Enabled=false
	u4 := acct.Users[3]
	if u4.Permissions.AllowResponses == nil {
		t.Fatal("expected AllowResponses on user 4 (non-nil even when disabled)")
	}
	if u4.Permissions.AllowResponses.Enabled {
		t.Error("expected Enabled=false for boolean false form")
	}
}
