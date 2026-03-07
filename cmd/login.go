package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"segura-cli/internal/config"
	"segura-cli/internal/webproxy"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Open senhasegura in browser with automatic login",
	Long: `Opens Chrome, navigates to senhasegura, and automatically fills in
credentials and TOTP code. The browser stays open for you to use.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(envFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
			return err
		}
		return webproxy.BrowserLogin(cfg)
	},
}

func init() {
	rootCmd.AddCommand(loginCmd)
}
