package cmd

import (
	"errors"

	"github.com/spf13/cobra"

	"hub/api"
)

var stackCmd = &cobra.Command{
	Use:   "stack <get> ...",
	Short: "List Base Stacks",
}

var stackGetCmd = &cobra.Command{
	Use:   "get [id]",
	Short: "Show a list of base stacks or details about the base stack",
	Long: `Show a list of all base stacks or details about
the particular base stack (specify Id)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return stack(args)
	},
}

func stack(args []string) error {
	if len(args) > 1 {
		return errors.New("Stack command has one optional argument - name of the base stack")
	}

	selector := ""
	if len(args) > 0 {
		selector = args[0]
	}
	api.BaseStacks(selector, jsonFormat)

	return nil
}

func init() {
	stackGetCmd.Flags().BoolVarP(&jsonFormat, "json", "j", false,
		"JSON output")
	stackCmd.AddCommand(stackGetCmd)
	apiCmd.AddCommand(stackCmd)
}
