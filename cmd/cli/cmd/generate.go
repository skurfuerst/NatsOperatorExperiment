package cmd

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	natsv1alpha1 "github.com/sandstorm/NatsAuthOperator/api/v1alpha1"
	"github.com/sandstorm/NatsAuthOperator/internal/natsconfig"
)

var generateCmd = &cobra.Command{
	Use:   "generate [paths...]",
	Short: "Generate NATS auth config from CRD YAML files",
	Long:  "Reads NatsCluster, NatsAccount, and NatsUser YAML files and produces NATS server auth config.",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runGenerate,
}

func init() {
	rootCmd.AddCommand(generateCmd)
}

func runGenerate(cmd *cobra.Command, args []string) error {
	res, err := loadFromPaths(args)
	if err != nil {
		return err
	}

	if len(res.Clusters) == 0 {
		return fmt.Errorf("no NatsCluster resources found")
	}

	// Generate config for each cluster
	for _, cluster := range res.Clusters {
		config := buildConfigForCluster(&cluster, res)
		configStr := natsconfig.Generate(config)
		fmt.Print(configStr)
	}

	return nil
}

func buildConfigForCluster(cluster *natsv1alpha1.NatsCluster, res *loadedResources) *natsconfig.NatsConfig {
	// Find accounts referencing this cluster
	accountsWithUsers := make([]natsconfig.AccountWithUsers, 0, len(res.Accounts))

	for _, acct := range res.Accounts {
		if acct.Spec.ClusterRef.Name != cluster.Name {
			continue
		}
		if acct.Namespace != "" && cluster.Namespace != "" && acct.Namespace != cluster.Namespace {
			continue
		}

		awu := natsconfig.AccountWithUsers{Account: acct}

		// Find users for this account
		for _, user := range res.Users {
			accountNs := user.Spec.AccountRef.Namespace
			if accountNs == "" {
				accountNs = user.Namespace
			}
			if user.Spec.AccountRef.Name != acct.Name {
				continue
			}
			if accountNs != "" && acct.Namespace != "" && accountNs != acct.Namespace {
				continue
			}

			// For CLI mode, use a placeholder public key
			awu.Users = append(awu.Users, natsconfig.UserWithPublicKey{
				User:      user,
				PublicKey: fmt.Sprintf("<nkey-for:%s>", user.Name),
			})
		}

		// Sort users by name
		sort.Slice(awu.Users, func(i, j int) bool {
			return awu.Users[i].User.Name < awu.Users[j].User.Name
		})

		accountsWithUsers = append(accountsWithUsers, awu)
	}

	return natsconfig.ConvertToNatsConfig(accountsWithUsers)
}
