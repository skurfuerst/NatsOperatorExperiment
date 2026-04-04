package cmd

import (
	"fmt"
	"regexp"

	"github.com/spf13/cobra"

	natsv1alpha1 "github.com/skurfuerst/natsoperatorexperiment/api/v1alpha1"
)

var validateCmd = &cobra.Command{
	Use:   "validate [paths...]",
	Short: "Validate NATS operator CRD manifests",
	Long: `Checks NatsCluster, NatsAccount, and NatsUser YAML files for errors
like missing refs, invalid regexes, and namespace violations.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runValidate,
}

func init() {
	rootCmd.AddCommand(validateCmd)
}

type validationError struct {
	Resource string
	Name     string
	Message  string
}

func (e validationError) String() string {
	return fmt.Sprintf("[%s/%s] %s", e.Resource, e.Name, e.Message)
}

func runValidate(cmd *cobra.Command, args []string) error {
	res, err := loadFromPaths(args)
	if err != nil {
		return err
	}

	var errs []validationError

	// Build lookup maps
	clusterNames := make(map[string]bool)
	for _, c := range res.Clusters {
		clusterNames[c.Name] = true
	}

	accountsByName := make(map[string]*natsv1alpha1.NatsAccount)
	for i := range res.Accounts {
		accountsByName[res.Accounts[i].Name] = &res.Accounts[i]
	}

	// Validate accounts
	for _, acct := range res.Accounts {
		// Check cluster ref exists
		if !clusterNames[acct.Spec.ClusterRef.Name] {
			errs = append(errs, validationError{
				Resource: "NatsAccount",
				Name:     acct.Name,
				Message:  fmt.Sprintf("clusterRef %q not found", acct.Spec.ClusterRef.Name),
			})
		}

		// Validate namespaceRegex patterns in userRules
		for i, rule := range acct.Spec.UserRules {
			if rule.NamespaceRegex != nil {
				if _, err := regexp.Compile(*rule.NamespaceRegex); err != nil {
					errs = append(errs, validationError{
						Resource: "NatsAccount",
						Name:     acct.Name,
						Message:  fmt.Sprintf("userRules[%d].namespaceRegex %q is invalid: %v", i, *rule.NamespaceRegex, err),
					})
				}
			}
		}
	}

	// Validate users
	for _, user := range res.Users {
		accountName := user.Spec.AccountRef.Name
		acct, found := accountsByName[accountName]
		if !found {
			errs = append(errs, validationError{
				Resource: "NatsUser",
				Name:     user.Name,
				Message:  fmt.Sprintf("accountRef %q not found", accountName),
			})
			continue
		}

		// Evaluate userRules for this user (offline: sameNamespace and namespaceRegex only;
		// namespaceLabels rules cannot be evaluated without a live cluster and are skipped).
		userNs := user.Namespace
		acctNs := acct.Namespace
		if userNs != "" && acctNs != "" {
			if len(acct.Spec.UserRules) == 0 {
				errs = append(errs, validationError{
					Resource: "NatsUser",
					Name:     user.Name,
					Message: fmt.Sprintf(
						"user (ns=%q) references account %q which has no userRules (access denied by default)",
						userNs, acct.Name),
				})
			} else {
				outcome := "deny" // default
				for _, rule := range acct.Spec.UserRules {
					var matched bool
					switch {
					case rule.SameNamespace != nil && *rule.SameNamespace:
						matched = userNs == acctNs
					case rule.NamespaceRegex != nil:
						re, err := regexp.Compile(*rule.NamespaceRegex)
						if err != nil {
							continue // already reported above
						}
						matched = re.MatchString(userNs)
					case rule.NamespaceLabels != nil:
						// Cannot evaluate without a live cluster; skip this rule.
						continue
					}
					if matched {
						outcome = string(rule.Action)
						break
					}
				}
				if outcome == "deny" {
					errs = append(errs, validationError{
						Resource: "NatsUser",
						Name:     user.Name,
						Message: fmt.Sprintf(
							"namespace %q denied by account %q userRules (or no evaluable rule matched)",
							userNs, acct.Name),
					})
				}
			}
		}
	}

	if len(errs) == 0 {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Validation passed: %d clusters, %d accounts, %d users\n",
			len(res.Clusters), len(res.Accounts), len(res.Users))
		return nil
	}

	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Validation found %d error(s):\n", len(errs))
	for _, e := range errs {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  - %s\n", e)
	}
	return fmt.Errorf("validation failed with %d error(s)", len(errs))
}
