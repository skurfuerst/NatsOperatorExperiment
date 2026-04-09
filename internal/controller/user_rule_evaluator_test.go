package controller

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	natsv1alpha1 "github.com/skurfuerst/natsoperatorexperiment/api/v1alpha1"
)

// mockNamespaceFetcher is a test double for NamespaceFetcher.
type mockNamespaceFetcher struct {
	namespaces map[string]*corev1.Namespace
}

func (m *mockNamespaceFetcher) GetNamespace(_ context.Context, name string) (*corev1.Namespace, error) {
	ns, ok := m.namespaces[name]
	if !ok {
		return nil, fmt.Errorf("namespace %q not found", name)
	}
	return ns, nil
}

func TestEvaluateUserRules_DefaultDeny(t *testing.T) {
	e := &UserRuleEvaluator{}
	allowed, err := e.Evaluate(context.Background(), nil, "any-ns", "account-ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Error("expected default deny when no rules defined")
	}
}

func TestEvaluateUserRules_EmptyRules(t *testing.T) {
	e := &UserRuleEvaluator{}
	allowed, err := e.Evaluate(context.Background(), []natsv1alpha1.UserRule{}, "any-ns", "account-ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Error("expected default deny when rules are empty")
	}
}

func TestEvaluateUserRules_SameNamespace_Grant(t *testing.T) {
	e := &UserRuleEvaluator{}
	rules := []natsv1alpha1.UserRule{
		{Action: natsv1alpha1.UserRuleActionGrant, SameNamespace: boolPtr(true)},
	}
	allowed, err := e.Evaluate(context.Background(), rules, "team-ns", "team-ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Error("expected grant for same-namespace user")
	}
}

func TestEvaluateUserRules_SameNamespace_DenyDifferentNamespace(t *testing.T) {
	e := &UserRuleEvaluator{}
	rules := []natsv1alpha1.UserRule{
		{Action: natsv1alpha1.UserRuleActionGrant, SameNamespace: boolPtr(true)},
	}
	allowed, err := e.Evaluate(context.Background(), rules, "other-ns", "account-ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Error("expected deny for user in different namespace")
	}
}

func TestEvaluateUserRules_NamespaceRegex_Grant(t *testing.T) {
	e := &UserRuleEvaluator{}
	rules := []natsv1alpha1.UserRule{
		{Action: natsv1alpha1.UserRuleActionGrant, NamespaceRegex: stringPtr("^team-.*$")},
	}
	allowed, err := e.Evaluate(context.Background(), rules, "team-alpha", "account-ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Error("expected grant for matching namespace regex")
	}
}

func TestEvaluateUserRules_NamespaceRegex_NoMatch(t *testing.T) {
	e := &UserRuleEvaluator{}
	rules := []natsv1alpha1.UserRule{
		{Action: natsv1alpha1.UserRuleActionGrant, NamespaceRegex: stringPtr("^team-.*$")},
	}
	allowed, err := e.Evaluate(context.Background(), rules, "other-ns", "account-ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Error("expected deny for non-matching namespace regex")
	}
}

func TestEvaluateUserRules_InvalidRegex(t *testing.T) {
	e := &UserRuleEvaluator{}
	rules := []natsv1alpha1.UserRule{
		{Action: natsv1alpha1.UserRuleActionGrant, NamespaceRegex: stringPtr("[invalid")},
	}
	_, err := e.Evaluate(context.Background(), rules, "any-ns", "account-ns")
	if err == nil {
		t.Error("expected error for invalid regex")
	}
}

func TestEvaluateUserRules_NamespaceLabels_Grant(t *testing.T) {
	fetcher := &mockNamespaceFetcher{
		namespaces: map[string]*corev1.Namespace{
			"labeled-ns": {
				ObjectMeta: metav1.ObjectMeta{
					Name:   "labeled-ns",
					Labels: map[string]string{"env": "staging"},
				},
			},
		},
	}
	e := &UserRuleEvaluator{NamespaceFetcher: fetcher}
	rules := []natsv1alpha1.UserRule{
		{Action: natsv1alpha1.UserRuleActionGrant, NamespaceLabels: map[string]string{"env": "staging"}},
	}
	allowed, err := e.Evaluate(context.Background(), rules, "labeled-ns", "account-ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Error("expected grant for namespace with matching labels")
	}
}

func TestEvaluateUserRules_NamespaceLabels_NoMatch(t *testing.T) {
	fetcher := &mockNamespaceFetcher{
		namespaces: map[string]*corev1.Namespace{
			"unlabeled-ns": {
				ObjectMeta: metav1.ObjectMeta{Name: "unlabeled-ns"},
			},
		},
	}
	e := &UserRuleEvaluator{NamespaceFetcher: fetcher}
	rules := []natsv1alpha1.UserRule{
		{Action: natsv1alpha1.UserRuleActionGrant, NamespaceLabels: map[string]string{"env": "staging"}},
	}
	allowed, err := e.Evaluate(context.Background(), rules, "unlabeled-ns", "account-ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Error("expected deny for namespace without matching labels")
	}
}

func TestEvaluateUserRules_NamespaceLabels_MultipleLabels(t *testing.T) {
	fetcher := &mockNamespaceFetcher{
		namespaces: map[string]*corev1.Namespace{
			"partial-ns": {
				ObjectMeta: metav1.ObjectMeta{
					Name:   "partial-ns",
					Labels: map[string]string{"env": "staging"},
				},
			},
		},
	}
	e := &UserRuleEvaluator{NamespaceFetcher: fetcher}
	rules := []natsv1alpha1.UserRule{
		{Action: natsv1alpha1.UserRuleActionGrant, NamespaceLabels: map[string]string{"env": "staging", "team": "alpha"}},
	}
	allowed, err := e.Evaluate(context.Background(), rules, "partial-ns", "account-ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Error("expected deny when namespace only has partial label match")
	}
}

func TestEvaluateUserRules_NamespaceFetchError(t *testing.T) {
	fetcher := &mockNamespaceFetcher{namespaces: map[string]*corev1.Namespace{}}
	e := &UserRuleEvaluator{NamespaceFetcher: fetcher}
	rules := []natsv1alpha1.UserRule{
		{Action: natsv1alpha1.UserRuleActionGrant, NamespaceLabels: map[string]string{"env": "staging"}},
	}
	_, err := e.Evaluate(context.Background(), rules, "missing-ns", "account-ns")
	if err == nil {
		t.Error("expected error when namespace fetch fails")
	}
}

func TestEvaluateUserRules_FirstMatchWins_DenyBeforeGrant(t *testing.T) {
	e := &UserRuleEvaluator{}
	rules := []natsv1alpha1.UserRule{
		{Action: natsv1alpha1.UserRuleActionDeny, NamespaceRegex: stringPtr("^blocked-.*$")},
		{Action: natsv1alpha1.UserRuleActionGrant, NamespaceRegex: stringPtr(".*")},
	}
	allowed, err := e.Evaluate(context.Background(), rules, "blocked-team", "account-ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Error("expected deny when deny rule matches first")
	}
}

func TestEvaluateUserRules_FirstMatchWins_GrantBeforeDeny(t *testing.T) {
	e := &UserRuleEvaluator{}
	rules := []natsv1alpha1.UserRule{
		{Action: natsv1alpha1.UserRuleActionGrant, NamespaceRegex: stringPtr("^team-.*$")},
		{Action: natsv1alpha1.UserRuleActionDeny, NamespaceRegex: stringPtr(".*")},
	}
	allowed, err := e.Evaluate(context.Background(), rules, "team-alpha", "account-ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Error("expected grant when grant rule matches first")
	}
}

func TestEvaluateUserRules_DenyAction(t *testing.T) {
	e := &UserRuleEvaluator{}
	rules := []natsv1alpha1.UserRule{
		{Action: natsv1alpha1.UserRuleActionDeny, SameNamespace: boolPtr(true)},
	}
	allowed, err := e.Evaluate(context.Background(), rules, "ns", "ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Error("expected deny when deny action matches")
	}
}

func TestEvaluateUserRules_UnknownRuleType(t *testing.T) {
	e := &UserRuleEvaluator{}
	// Rule with no selector set — should not match
	rules := []natsv1alpha1.UserRule{
		{Action: natsv1alpha1.UserRuleActionGrant},
	}
	allowed, err := e.Evaluate(context.Background(), rules, "any-ns", "account-ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Error("expected deny when rule has no selector")
	}
}

// ValidateRegexRules tests
func TestValidateRegexRules_Valid(t *testing.T) {
	e := &UserRuleEvaluator{}
	rules := []natsv1alpha1.UserRule{
		{Action: natsv1alpha1.UserRuleActionGrant, NamespaceRegex: stringPtr("^team-.*$")},
		{Action: natsv1alpha1.UserRuleActionDeny, SameNamespace: boolPtr(true)},
	}
	err := e.ValidateRegexRules(rules)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRegexRules_Invalid(t *testing.T) {
	e := &UserRuleEvaluator{}
	rules := []natsv1alpha1.UserRule{
		{Action: natsv1alpha1.UserRuleActionGrant, NamespaceRegex: stringPtr("[invalid")},
	}
	err := e.ValidateRegexRules(rules)
	if err == nil {
		t.Error("expected error for invalid regex in validation")
	}
}

func TestValidateRegexRules_NoRegex(t *testing.T) {
	e := &UserRuleEvaluator{}
	rules := []natsv1alpha1.UserRule{
		{Action: natsv1alpha1.UserRuleActionGrant, SameNamespace: boolPtr(true)},
	}
	err := e.ValidateRegexRules(rules)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Helper functions for test readability (avoid colliding with Ginkgo test helpers)
func boolPtr(v bool) *bool       { return &v }
func stringPtr(v string) *string { return &v }
