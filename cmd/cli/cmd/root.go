package cmd

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "nats-operator-cli",
	Short: "CLI for NATS operator config generation and validation",
}

func Execute() error {
	return rootCmd.Execute()
}
