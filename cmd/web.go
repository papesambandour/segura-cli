package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"segura-cli/internal/config"
	"segura-cli/internal/webserver"
)

var webPort int

var webCmd = &cobra.Command{
	Use:   "web",
	Short: "Start the SEGURA web terminal server",
	Long:  `Starts a local web server providing a browser-based terminal interface to senhasegura managed devices.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(envFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
			return err
		}

		return webserver.Start(cfg, webPort)
	},
}

func init() {
	webCmd.Flags().IntVar(&webPort, "port", 8080, "port to listen on")
	rootCmd.AddCommand(webCmd)
}
