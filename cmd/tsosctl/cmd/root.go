package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/zwolsman/tailscale-os/cmd/tsosctl/cmd/tsos"
)

// rootCmd represents the base command when called without any subcommands.
var rootCmd = &cobra.Command{
	Use:               "tsosctl",
	Short:             "A CLI for out-of-band management of Tailscale nodes created by TSOS",
	Long:              ``,
	SilenceErrors:     true,
	SilenceUsage:      true,
	DisableAutoGenTag: true,
}

func Execute() error {
	err := rootCmd.ExecuteContext(context.Background())
	if err != nil {
		errorString := err.Error()
		fmt.Fprintln(os.Stderr, errorString)
	}

	return err
}

// init adds all child commands to the base command.
func init() {
	for _, cmd := range tsos.Commands {
		rootCmd.AddCommand(cmd)
	}
}
