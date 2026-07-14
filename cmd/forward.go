package cmd

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"segura-cli/internal/config"
	"segura-cli/internal/sftpclient"
)

var forwardSpec string

// forwardCmd sets up a local port-forward to a service running on the device,
// tunneled through the gateway's direct-tcpip channel. Useful to reach a DB,
// admin UI or any TCP service bound on the device's loopback.
var forwardCmd = &cobra.Command{
	Use:   "forward -L [bind:]<lport>:<host>:<rport> <credential>@<device>",
	Short: "Forward a local port to a service on the device (through the gateway)",
	Long: `Forwards a local TCP port to a service reachable from the target device,
tunneled through the senhasegura gateway.

The gateway restricts the tunnel to the device's own network — target the
device loopback (127.0.0.1) to reach services bound there (databases, admin
UIs, etc.). Runs until interrupted (Ctrl-C).

Examples:
  # Expose the device's MySQL/MariaDB locally on 3306
  segura forward -L 3306:127.0.0.1:3306 dbuser@SRV-DB1
  # Local 8080 -> device's web admin on 80
  segura forward -L 8080:127.0.0.1:80 admin@SRV-WEB1`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		if forwardSpec == "" {
			return fmt.Errorf("missing -L spec (e.g. -L 3306:127.0.0.1:3306)")
		}
		bind, lport, remote, err := parseForwardSpec(forwardSpec)
		if err != nil {
			return err
		}
		credential, device, err := parseTarget(args[0])
		if err != nil {
			return err
		}

		cfg, err := config.Load(envFile)
		if err != nil {
			return fmt.Errorf("configuration error: %w", err)
		}

		fmt.Printf("Connecting to %s@%s via %s...\n", credential, device, cfg.Host)
		client, err := sftpclient.DialClient(cfg, credential, device)
		if err != nil {
			return fmt.Errorf("gateway connection failed: %w", err)
		}
		defer client.Close()

		localAddr := net.JoinHostPort(bind, lport)
		ln, err := net.Listen("tcp", localAddr)
		if err != nil {
			return fmt.Errorf("cannot listen on %s: %w", localAddr, err)
		}
		defer ln.Close()

		recordRecent(credential, device, "")
		fmt.Printf("Forwarding  %s  ->  %s  (on %s@%s)\n", localAddr, remote, credential, device)
		fmt.Println("Press Ctrl-C to stop.")

		// Close the listener on Ctrl-C so Accept unblocks and we exit cleanly.
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		go func() { <-sig; fmt.Println("\nStopping..."); ln.Close() }()

		for {
			local, err := ln.Accept()
			if err != nil {
				return nil // listener closed (Ctrl-C)
			}
			go handleForward(client, local, remote)
		}
	},
}

// handleForward bridges one accepted local connection to the remote service
// through a fresh direct-tcpip channel.
func handleForward(client sshDialer, local net.Conn, remote string) {
	defer local.Close()
	ch, err := client.Dial("tcp", remote)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forward: cannot reach %s: %v\n", remote, err)
		return
	}
	defer ch.Close()

	done := make(chan struct{}, 2)
	go func() { io.Copy(ch, local); done <- struct{}{} }()
	go func() { io.Copy(local, ch); done <- struct{}{} }()
	<-done
}

// sshDialer is the subset of *ssh.Client used here (aids testing/clarity).
type sshDialer interface {
	Dial(network, addr string) (net.Conn, error)
}

// parseForwardSpec parses "[bind:]lport:rhost:rport" (ssh -L syntax).
func parseForwardSpec(spec string) (bind, lport, remote string, err error) {
	parts := strings.Split(spec, ":")
	switch len(parts) {
	case 3: // lport:rhost:rport
		bind = "127.0.0.1"
		lport, remote = parts[0], net.JoinHostPort(parts[1], parts[2])
	case 4: // bind:lport:rhost:rport
		bind, lport, remote = parts[0], parts[1], net.JoinHostPort(parts[2], parts[3])
	default:
		return "", "", "", fmt.Errorf("invalid -L spec %q: want [bind:]lport:host:rport", spec)
	}
	if lport == "" || parts[len(parts)-1] == "" {
		return "", "", "", fmt.Errorf("invalid -L spec %q: empty port", spec)
	}
	return bind, lport, remote, nil
}

func init() {
	forwardCmd.Flags().StringVarP(&forwardSpec, "local", "L", "", "Local forward: [bind:]lport:host:rport")
	rootCmd.AddCommand(forwardCmd)
}
