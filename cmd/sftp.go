package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"segura-cli/internal/config"
	"segura-cli/internal/sftpclient"
)

var sftpCommand string

// sftpCmd opens an interactive SFTP file browser to a device through senhasegura.
var sftpCmd = &cobra.Command{
	Use:   "sftp <credential>@<device>",
	Short: "Browse and transfer files over SFTP (interactive)",
	Long: `Opens an interactive SFTP session to a target device through the senhasegura
PAM gateway — browse, download and upload files without leaving the terminal.

Interactive commands:
  ls [path]           list a directory        cd <path>     change directory
  ll [path]           long listing            pwd           print remote dir
  get <remote> [local]  download a file       put <local> [remote]  upload a file
  cat <file>          print a text file       mkdir <path>  create a directory
  rm <path>           delete (recursive)      mv <old> <new>  rename / move
  lcd <dir> / lpwd / lls   local directory ops
  help                show commands           exit / quit   leave (Ctrl-D)

Run a single command non-interactively with -c:
  segura sftp root@web01 -c "ls /var/log"

Examples:
  segura sftp root@192.168.1.10
  segura sftp admin@webserver01 -c "get /etc/hosts ./hosts"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		credential, device, err := parseTarget(args[0])
		if err != nil {
			return err
		}
		cfg, err := config.Load(envFile)
		if err != nil {
			return fmt.Errorf("configuration error: %w", err)
		}

		fmt.Printf("Opening SFTP session to %s@%s via %s...\n", credential, device, cfg.Host)
		mgr := sftpclient.NewManager(cfg)
		sess, err := mgr.GetSession(credential, device)
		if err != nil {
			return fmt.Errorf("could not open SFTP session: %w", err)
		}

		// Remember this target for the connect picker too (best-effort).
		recordRecent(credential, device, "")

		br := &sftpBrowser{sess: sess, cfg: cfg}
		cwd, err := sess.Getwd()
		if err != nil || cwd == "" {
			cwd = "."
		}
		br.cwd = cwd
		br.lcwd, _ = os.Getwd()

		if sftpCommand != "" {
			return br.dispatch(splitArgs(sftpCommand))
		}
		fmt.Printf("Connected. Remote dir: %s  (type 'help', 'exit' to quit)\n", br.cwd)
		return br.repl()
	},
}

// sftpBrowser holds the interactive session state.
type sftpBrowser struct {
	sess *sftpclient.Session
	cfg  *config.Config
	cwd  string // remote working dir (absolute)
	lcwd string // local working dir
}

func (b *sftpBrowser) repl() error {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for {
		fmt.Printf("sftp:%s> ", b.cwd)
		if !sc.Scan() {
			fmt.Println()
			return nil // EOF / Ctrl-D
		}
		fields := splitArgs(sc.Text())
		if len(fields) == 0 {
			continue
		}
		if c := strings.ToLower(fields[0]); c == "exit" || c == "quit" {
			return nil
		}
		if err := b.dispatch(fields); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
	}
}

// dispatch runs a single parsed command line.
func (b *sftpBrowser) dispatch(fields []string) error {
	if len(fields) == 0 {
		return nil
	}
	cmd, args := strings.ToLower(fields[0]), fields[1:]
	switch cmd {
	case "help", "?":
		b.help()
	case "ls":
		return b.ls(args, false)
	case "ll", "dir":
		return b.ls(args, true)
	case "cd":
		return b.cd(args)
	case "pwd":
		fmt.Println(b.cwd)
	case "get", "download":
		return b.get(args)
	case "put", "upload":
		return b.put(args)
	case "cat":
		return b.cat(args)
	case "mkdir":
		return b.mkdir(args)
	case "rm", "del", "delete":
		return b.rm(args)
	case "mv", "rename":
		return b.mv(args)
	case "lcd":
		return b.lcd(args)
	case "lpwd":
		fmt.Println(b.lcwd)
	case "lls":
		return b.lls(args)
	case "exit", "quit":
		os.Exit(0)
	default:
		return fmt.Errorf("unknown command %q (try 'help')", cmd)
	}
	return nil
}

// rpath resolves p against the remote cwd (absolute or relative).
func (b *sftpBrowser) rpath(p string) string {
	if p == "" {
		return b.cwd
	}
	if path.IsAbs(p) {
		return path.Clean(p)
	}
	return path.Clean(path.Join(b.cwd, p))
}

// lpath resolves p against the local cwd.
func (b *sftpBrowser) lpath(p string) string {
	if p == "" {
		return b.lcwd
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(b.lcwd, p)
}

func (b *sftpBrowser) ls(args []string, long bool) error {
	target := b.cwd
	if len(args) > 0 {
		target = b.rpath(args[0])
	}
	entries, err := b.sess.ListDir(target)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir // dirs first
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	for _, e := range entries {
		name := e.Name
		if e.IsDir {
			name += "/"
		}
		if long {
			ts := e.ModTime
			if len(ts) >= 16 {
				ts = strings.Replace(ts[:16], "T", " ", 1)
			}
			fmt.Printf("%-11s %10s  %-16s  %s\n", e.Mode, humanSize(e.Size), ts, name)
		} else {
			fmt.Println(name)
		}
	}
	return nil
}

func (b *sftpBrowser) cd(args []string) error {
	if len(args) == 0 {
		home, err := b.sess.Getwd()
		if err == nil && home != "" {
			b.cwd = home
		}
		return nil
	}
	target := b.rpath(args[0])
	info, err := b.sess.Stat(target)
	if err != nil {
		return fmt.Errorf("cd: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("cd: %s is not a directory", target)
	}
	b.cwd = target
	return nil
}

func (b *sftpBrowser) get(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: get <remote> [local]")
	}
	remote := b.rpath(args[0])
	local := b.lpath(path.Base(remote))
	if len(args) > 1 {
		local = b.lpath(args[1])
	}
	// If local is an existing dir, place the file inside it.
	if fi, err := os.Stat(local); err == nil && fi.IsDir() {
		local = filepath.Join(local, path.Base(remote))
	}
	f, err := os.Create(local)
	if err != nil {
		return err
	}
	defer f.Close()
	n, _, err := b.sess.Download(remote, f)
	if err != nil {
		return err
	}
	fmt.Printf("downloaded %s -> %s (%s)\n", remote, local, humanSize(n))
	return nil
}

func (b *sftpBrowser) put(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: put <local> [remote]")
	}
	local := b.lpath(args[0])
	remote := b.rpath(path.Base(local))
	if len(args) > 1 {
		remote = b.rpath(args[1])
		// If remote is an existing dir, place the file inside it.
		if info, err := b.sess.Stat(remote); err == nil && info.IsDir() {
			remote = path.Join(remote, filepath.Base(local))
		}
	}
	f, err := os.Open(local)
	if err != nil {
		return err
	}
	defer f.Close()

	n, err := b.sess.Upload(remote, f)
	if err != nil && sftpclient.IsPermissionError(err) {
		// retry with sudo (rewind first)
		if _, serr := f.Seek(0, io.SeekStart); serr == nil {
			fmt.Println("permission denied — retrying with sudo...")
			n, err = b.sess.UploadSudo(remote, f, b.cfg.Password)
		}
	}
	if err != nil {
		return err
	}
	fmt.Printf("uploaded %s -> %s (%s)\n", local, remote, humanSize(n))
	return nil
}

func (b *sftpBrowser) cat(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cat <file>")
	}
	data, err := b.sess.ReadFile(b.rpath(args[0]), 2*1024*1024)
	if err != nil {
		return err
	}
	os.Stdout.Write(data)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		fmt.Println()
	}
	return nil
}

func (b *sftpBrowser) mkdir(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: mkdir <path>")
	}
	target := b.rpath(args[0])
	err := b.sess.Mkdir(target, false, "")
	if err != nil && sftpclient.IsPermissionError(err) {
		fmt.Println("permission denied — retrying with sudo...")
		err = b.sess.Mkdir(target, true, b.cfg.Password)
	}
	if err != nil {
		return err
	}
	fmt.Printf("created %s\n", target)
	return nil
}

func (b *sftpBrowser) rm(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: rm <path>")
	}
	target := b.rpath(args[0])
	err := b.sess.Remove(target, false, "")
	if err != nil && sftpclient.IsPermissionError(err) {
		fmt.Println("permission denied — retrying with sudo...")
		err = b.sess.Remove(target, true, b.cfg.Password)
	}
	if err != nil {
		return err
	}
	fmt.Printf("removed %s\n", target)
	return nil
}

func (b *sftpBrowser) mv(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: mv <old> <new>")
	}
	oldp, newp := b.rpath(args[0]), b.rpath(args[1])
	err := b.sess.Rename(oldp, newp, false, "")
	if err != nil && sftpclient.IsPermissionError(err) {
		fmt.Println("permission denied — retrying with sudo...")
		err = b.sess.Rename(oldp, newp, true, b.cfg.Password)
	}
	if err != nil {
		return err
	}
	fmt.Printf("moved %s -> %s\n", oldp, newp)
	return nil
}

func (b *sftpBrowser) lcd(args []string) error {
	if len(args) == 0 {
		home, _ := os.UserHomeDir()
		b.lcwd = home
		return nil
	}
	target := b.lpath(args[0])
	fi, err := os.Stat(target)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("lcd: %s is not a directory", target)
	}
	b.lcwd = target
	return nil
}

func (b *sftpBrowser) lls(args []string) error {
	target := b.lcwd
	if len(args) > 0 {
		target = b.lpath(args[0])
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		fmt.Println(name)
	}
	return nil
}

func (b *sftpBrowser) help() {
	fmt.Print(`Remote:  ls [p]  ll [p]  cd <p>  pwd  get <r> [l]  put <l> [r]  cat <f>
         mkdir <p>  rm <p>  mv <old> <new>
Local:   lcd <d>  lpwd  lls [d]
Other:   help  exit
`)
}

// humanSize formats a byte count compactly.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + "B"
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// splitArgs splits a command line on whitespace, honoring single/double quotes so
// paths with spaces work (e.g. get "/var/My Files/x").
func splitArgs(line string) []string {
	var out []string
	var cur strings.Builder
	inWord := false
	var quote rune
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
			inWord = true
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == ' ' || r == '\t':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out
}

func init() {
	sftpCmd.Flags().StringVarP(&sftpCommand, "command", "c", "", "Run a single SFTP command and exit")
	rootCmd.AddCommand(sftpCmd)
}
