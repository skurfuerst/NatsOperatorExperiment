package main

import (
	"os"

	"github.com/skurfuerst/natsoperatorexperiment/cmd/cli/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
