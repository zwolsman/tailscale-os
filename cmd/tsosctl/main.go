package main

import (
	"os"

	"github.com/zwolsman/tailscale-os/cmd/tsosctl/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
