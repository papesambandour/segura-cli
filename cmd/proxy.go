package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"segura-cli/internal/config"
	"segura-cli/internal/sftpclient"
)

// proxyCmd is the stdio backend used as an ssh ProxyCommand: it opens a
// direct-tcpip channel through the gateway to a port on the device (default the
// device's own sshd on 127.0.0.1:22) and pipes it to stdin/stdout, so native
// ssh/scp/rsync/git can reach the device through the PAM gateway.
//
// Hidden because it's plumbing — users get it wired up via `segura ssh-config`.
var proxyCmd = &cobra.Command{
	Use:    "proxy <credential>@<device> [host:port]",
	Short:  "Pipe stdio to a device port through the gateway (ssh ProxyCommand backend)",
	Hidden: true,
	Args:   cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		credential, device, err := parseTarget(args[0])
		if err != nil {
			return err
		}
		target := "127.0.0.1:22"
		if len(args) == 2 {
			target = normalizeHostPort(args[1])
		}

		cfg, err := config.Load(envFile)
		if err != nil {
			return fmt.Errorf("configuration error: %w", err)
		}

		// Logs go to stderr (sftpclient uses the std logger → stderr), keeping
		// stdout clean for the byte pipe.
		client, err := sftpclient.DialClient(cfg, credential, device)
		if err != nil {
			return fmt.Errorf("gateway connection failed: %w", err)
		}
		defer client.Close()

		ch, err := client.Dial("tcp", target)
		if err != nil {
			return fmt.Errorf("could not open channel to %s: %w", target, err)
		}
		defer ch.Close()

		// Pipe both directions; return as soon as either side closes.
		done := make(chan struct{}, 2)
		go func() { io.Copy(ch, os.Stdin); done <- struct{}{} }()
		go func() { io.Copy(os.Stdout, ch); done <- struct{}{} }()
		<-done
		return nil
	},
}

// normalizeHostPort accepts "host:port" or a bare "port" (→ 127.0.0.1:port).
func normalizeHostPort(s string) string {
	if !strings.Contains(s, ":") {
		return "127.0.0.1:" + s
	}
	return s
}

func init() {
	rootCmd.AddCommand(proxyCmd)
}
