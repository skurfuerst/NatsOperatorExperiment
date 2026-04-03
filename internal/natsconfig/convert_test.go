package natsconfig

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	natsv1alpha1 "github.com/skurfuerst/natsoperatorexperiment/api/v1alpha1"
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
	if u2.Permissions != nil {
		t.Error("expected nil permissions on user 2")
	}
}
