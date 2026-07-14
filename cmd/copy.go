package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"segura-cli/internal/config"
	"segura-cli/internal/proxy"
)

var (
	copyPort      int
	copyRecursive bool
)

var copyCmd = &cobra.Command{
	Use:   "copy <src> <dest>",
	Short: "Copy files via SCP through senhasegura",
	Long: `Transfers files or directories via SCP through the senhasegura Terminal Proxy.
Use the credential@device:/path syntax for remote paths. Local directories are
copied recursively automatically; use -r to recurse when downloading a remote
directory.

Examples:
  segura copy ./local-file.txt root@192.168.1.10:/tmp/
  segura copy ./deploy/ root@192.168.1.10:/tmp/deploy/
  segura copy root@192.168.1.10:/var/log/syslog ./logs/
  segura copy -r root@192.168.1.10:/etc/app ./app-backup/
  segura copy ./config.yaml admin@webserver01:/etc/app/ --port 2222`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		src := args[0]
		dest := args[1]

		if !proxy.IsRemotePath(src) && !proxy.IsRemotePath(dest) {
			return fmt.Errorf("at least one of source or destination must be a remote path (credential@device:/path)")
		}

		cfg, err := config.Load(envFile)
		if err != nil {
			return fmt.Errorf("configuration error: %w", err)
		}

		if proxy.IsRemotePath(src) {
			fmt.Printf("Downloading from %s via %s...\n", src, cfg.Host)
		} else {
			fmt.Printf("Uploading to %s via %s...\n", dest, cfg.Host)
		}

		if err := proxy.CopyFile(cfg, src, dest, copyPort, copyRecursive); err != nil {
			return err
		}

		fmt.Println("Transfer complete.")
		return nil
	},
}

func init() {
	copyCmd.Flags().IntVar(&copyPort, "port", 22, "SSH port on the senhasegura host")
	copyCmd.Flags().BoolVarP(&copyRecursive, "recursive", "r", false, "copy directories recursively (auto-enabled for local directories)")
	rootCmd.AddCommand(copyCmd)
}
