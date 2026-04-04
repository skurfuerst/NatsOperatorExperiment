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

		// Validate regex patterns
		for _, pattern := range acct.Spec.AllowedUserNamespaces {
			if _, err := regexp.Compile(pattern); err != nil {
				errs = append(errs, validationError{
					Resource: "NatsAccount",
					Name:     acct.Name,
					Message:  fmt.Sprintf("invalid allowedUserNamespaces regex %q: %v", pattern, err),
				})
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

		// Check cross-namespace is allowed
		userNs := user.Namespace
		acctNs := acct.Namespace
		if userNs != "" && acctNs != "" && userNs != acctNs {
			if len(acct.Spec.AllowedUserNamespaces) == 0 {
				errs = append(errs, validationError{
					Resource: "NatsUser",
					Name:     user.Name,
					Message: fmt.Sprintf(
						"cross-namespace user (ns=%q) but account %q has no allowedUserNamespaces",
						userNs, acct.Name),
				})
			} else {
				allowed := false
				for _, pattern := range acct.Spec.AllowedUserNamespaces {
					re, err := regexp.Compile(pattern)
					if err != nil {
						continue
					}
					if re.MatchString(userNs) {
						allowed = true
						break
					}
				}
				if !allowed {
					errs = append(errs, validationError{
						Resource: "NatsUser",
						Name:     user.Name,
						Message:  fmt.Sprintf("namespace %q not matched by account's allowedUserNamespaces", userNs),
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
