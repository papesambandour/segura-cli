package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

var (
	envFile    string
	daemonPort int
	flagStart  bool
	flagStop   bool
	flagRestart bool

	version    = "dev"
	commit     = "none"
	buildDate  = "unknown"
	githubRepo = "papesambandour/segura-cli"
)

func SetVersionInfo(v, c, d, r string) {
	version = v
	commit = c
	buildDate = d
	githubRepo = r
}

func pidDir() string {
	return filepath.Join(os.Getenv("HOME"), ".segura")
}

func pidFile() string {
	return filepath.Join(pidDir(), "segura.pid")
}

func readPID() (int, error) {
	data, err := os.ReadFile(pidFile())
	if err != nil {
		return 0, fmt.Errorf("no PID file found (%s): %w", pidFile(), err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("invalid PID in %s: %w", pidFile(), err)
	}
	return pid, nil
}

func writePID(pid int) error {
	if err := os.MkdirAll(pidDir(), 0755); err != nil {
		return fmt.Errorf("cannot create %s: %w", pidDir(), err)
	}
	return os.WriteFile(pidFile(), []byte(strconv.Itoa(pid)), 0644)
}

func daemonStop() error {
	pid, err := readPID()
	if err != nil {
		return err
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		os.Remove(pidFile())
		return fmt.Errorf("process %d not found: %w", pid, err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		os.Remove(pidFile())
		return fmt.Errorf("failed to stop process %d: %w", pid, err)
	}

	os.Remove(pidFile())
	fmt.Printf("Segura web server (PID %d) stopped.\n", pid)
	return nil
}

func daemonStart(port int) error {
	// Check if already running
	if pid, err := readPID(); err == nil {
		if process, err := os.FindProcess(pid); err == nil {
			if err := process.Signal(syscall.Signal(0)); err == nil {
				return fmt.Errorf("segura web server already running (PID %d). Use --stop first or --restart", pid)
			}
		}
		// Stale PID file, clean up
		os.Remove(pidFile())
	}

	// Load .env so child process inherits the environment
	if envFile != "" {
		_ = godotenv.Load(envFile)
	} else {
		_ = godotenv.Load()
	}

	// Use current executable (ensures we run the same version)
	binary, err := os.Executable()
	if err != nil {
		binary, err = exec.LookPath("segura")
		if err != nil {
			return fmt.Errorf("cannot find segura binary: %w", err)
		}
	}

	args := []string{"web", "--port", strconv.Itoa(port)}
	if envFile != "" {
		args = append(args, "--env", envFile)
	}

	cmd := exec.Command(binary, args...)
	// Log daemon output for debugging
	logFile, err := os.OpenFile(filepath.Join(pidDir(), "segura.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		cmd.Stdout = nil
		cmd.Stderr = nil
	} else {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	cmd.Stdin = nil
	// Detach from parent process
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	if err := writePID(cmd.Process.Pid); err != nil {
		return fmt.Errorf("server started but failed to write PID file: %w", err)
	}

	fmt.Printf("Segura web server started on port %d (PID %d).\n", port, cmd.Process.Pid)
	fmt.Printf("PID file: %s\n", pidFile())
	fmt.Printf("Open http://localhost:%d in your browser.\n", port)
	return nil
}

var rootCmd = &cobra.Command{
	Use:   "segura",
	Short: "SEGURA-CLI — connect to devices through senhasegura PAM",
	Long:  `A CLI tool for SSH and SCP operations through the senhasegura Privileged Access Management platform.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if flagRestart || flagStop || flagStart {
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
		}
		if flagRestart {
			_ = daemonStop()
			return daemonStart(daemonPort)
		}
		if flagStop {
			return daemonStop()
		}
		if flagStart {
			return daemonStart(daemonPort)
		}
		// No daemon flag — show help
		return cmd.Help()
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("segura %s\n", version)
		fmt.Printf("  commit:  %s\n", commit)
		fmt.Printf("  built:   %s\n", buildDate)
	},
}

func Execute() {
	rootCmd.Version = version
	rootCmd.AddCommand(versionCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&envFile, "env", "", "path to .env file (default: .env in current directory)")
	rootCmd.Flags().BoolVar(&flagStart, "start", false, "start the web server as a background daemon")
	rootCmd.Flags().BoolVar(&flagStop, "stop", false, "stop the background web server daemon")
	rootCmd.Flags().BoolVar(&flagRestart, "restart", false, "restart the background web server daemon")
	rootCmd.Flags().IntVar(&daemonPort, "port", 8080, "port for the daemon web server (used with --start)")
}
