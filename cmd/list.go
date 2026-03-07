package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"segura-cli/internal/config"
	"segura-cli/internal/webproxy"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List available credentials from senhasegura",
	Long: `Authenticates to senhasegura and displays all available
credentials you can connect to.

Examples:
  segura list
  segura --env /path/to/.env list`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(envFile)
		if err != nil {
			return fmt.Errorf("configuration error: %w", err)
		}

		fmt.Println("Authenticating to senhasegura...")
		credentials, err := webproxy.ListCredentials(cfg)
		if err != nil {
			return err
		}

		if len(credentials) == 0 {
			fmt.Println("No credentials found.")
			return nil
		}

		fmt.Printf("\nAvailable credentials (%d):\n\n", len(credentials))
		fmt.Printf("  %-20s %-30s %s\n", "Credential", "Device", "Command")
		fmt.Printf("  %-20s %-30s %s\n", "----------", "------", "-------")
		for _, c := range credentials {
			device := c.IP
			if c.Device != "" {
				device = fmt.Sprintf("%s (%s)", c.Device, c.IP)
			}
			command := fmt.Sprintf("segura connect %s@%s", c.Username, c.IP)
			fmt.Printf("  %-20s %-30s %s\n", c.Username, device, command)
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
}
