package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/agilestacks/hub/cmd/hub/api"
	"github.com/agilestacks/hub/cmd/hub/config"
	"github.com/agilestacks/hub/cmd/hub/util"
)

var (
	loginUsername string
	loginPassword string
)

var loginCmd = &cobra.Command{
	Use:   "login [https://api.agilestacks.io] [-u email] [-p password]",
	Short: "Obtain Login Token for subsequent Hub API calls",
	Long: `Sign-in to Hub API service to obtain a Login Token.

The token is used to call API service to manage Secrets, print Environment or Stack Instance
details, etc. If API URL is not given on command line then it's read from HUB_API env.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return login(args)
	},
}

func login(args []string) error {
	if len(args) > 1 {
		return errors.New("Login command has one optional argument - Hub API service base URL")
	}

	apiBaseUrl := ""
	if len(args) > 0 {
		apiBaseUrl = args[0]
	}
	if apiBaseUrl == "" {
		apiBaseUrl = config.ApiBaseUrl
	}
	if apiBaseUrl == "" {
		return fmt.Errorf("Hub API service base URL not provided - set %s env or supply command line argument",
			envVarNameHubApi)
	}
	loginUsername = util.AskInput(loginUsername, "Username")
	loginPassword = util.AskPassword(loginPassword, "Password")
	api.Login(apiBaseUrl, loginUsername, loginPassword)

	return nil
}

func init() {
	loginCmd.Flags().StringVarP(&loginUsername, "username", "u", "", "SuperHub username")
	loginCmd.Flags().StringVarP(&loginPassword, "password", "p", "", "SuperHub password")
	RootCmd.AddCommand(loginCmd)
}
