package controller

import (
	"context"
	"fmt"
	"regexp"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	natsv1alpha1 "github.com/skurfuerst/natsoperatorexperiment/api/v1alpha1"
)

// NamespaceFetcher retrieves a Namespace by name.
type NamespaceFetcher interface {
	GetNamespace(ctx context.Context, name string) (*corev1.Namespace, error)
}

// clientNamespaceFetcher adapts a client.Reader to NamespaceFetcher.
type clientNamespaceFetcher struct {
	client.Reader
}

func (f *clientNamespaceFetcher) GetNamespace(ctx context.Context, name string) (*corev1.Namespace, error) {
	ns := &corev1.Namespace{}
	if err := f.Get(ctx, types.NamespacedName{Name: name}, ns); err != nil {
		return nil, err
	}
	return ns, nil
}

// UserRuleEvaluator evaluates ordered user rules against a user's namespace.
type UserRuleEvaluator struct {
	NamespaceFetcher NamespaceFetcher
}

// ValidateRegexRules checks that all regex rules in the list compile successfully.
func (e *UserRuleEvaluator) ValidateRegexRules(rules []natsv1alpha1.UserRule) error {
	for _, rule := range rules {
		if rule.NamespaceRegex != nil {
			if _, err := regexp.Compile(*rule.NamespaceRegex); err != nil {
				return fmt.Errorf("invalid namespaceRegex %q: %w", *rule.NamespaceRegex, err)
			}
		}
	}
	return nil
}

// Evaluate evaluates the ordered user rules against userNamespace.
// Returns (true, nil) if a grant rule matches first.
// Returns (false, nil) if a deny rule matches first, or no rules match (default deny).
// Returns (false, err) if a namespace fetch for label matching fails.
func (e *UserRuleEvaluator) Evaluate(
	ctx context.Context,
	rules []natsv1alpha1.UserRule,
	userNamespace string,
	accountNamespace string,
) (bool, error) {
	for _, rule := range rules {
		matched, err := e.ruleMatchesUser(ctx, rule, userNamespace, accountNamespace)
		if err != nil {
			return false, err
		}
		if matched {
			return rule.Action == natsv1alpha1.UserRuleActionGrant, nil
		}
	}
	return false, nil // no match → default deny
}

func (e *UserRuleEvaluator) ruleMatchesUser(
	ctx context.Context,
	rule natsv1alpha1.UserRule,
	userNamespace string,
	accountNamespace string,
) (bool, error) {
	switch {
	case rule.SameNamespace != nil && *rule.SameNamespace:
		return userNamespace == accountNamespace, nil
	case rule.NamespaceRegex != nil:
		re, err := regexp.Compile(*rule.NamespaceRegex)
		if err != nil {
			return false, fmt.Errorf("invalid namespaceRegex %q: %w", *rule.NamespaceRegex, err)
		}
		return re.MatchString(userNamespace), nil
	case rule.NamespaceLabels != nil:
		ns, err := e.NamespaceFetcher.GetNamespace(ctx, userNamespace)
		if err != nil {
			return false, fmt.Errorf("fetching namespace %q: %w", userNamespace, err)
		}
		for k, v := range rule.NamespaceLabels {
			if ns.Labels[k] != v {
				return false, nil
			}
		}
		return true, nil
	default:
		return false, nil
	}
}
