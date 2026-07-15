# SEGURA-CLI

A CLI + web app for connecting to remote devices through the **senhasegura PAM** (Privileged Access Management) platform.

SSH terminal, an interactive fuzzy picker, SCP/SFTP file transfer, a multi-tab
browser terminal, local port-forwarding, and native `ssh`/`scp`/`rsync` support —
all through your PAM gateway.

![SEGURA Demo](docs/screenshots/segura-demo.gif)

---

## Installation

### One-line install (macOS / Linux)

```bash
curl -sSL https://papesambandour.github.io/segura-cli/install.sh | bash
```

This detects your OS/architecture, downloads the latest release, installs to `~/.local/bin/segura`, and sets up shell completion.

### Interactive install

```bash
curl -O https://papesambandour.github.io/segura-cli/install.sh
bash install.sh
```

The interactive mode prompts for your senhasegura credentials and writes them to your shell RC file.

### Manual download

Download the binary for your platform from [GitHub Releases](https://github.com/papesambandour/segura-cli/releases):

| Platform | Binary |
|----------|--------|
| macOS Apple Silicon | `segura-darwin-arm64` |
| macOS Intel | `segura-darwin-amd64` |
| Linux x86_64 | `segura-linux-amd64` |
| Linux ARM64 | `segura-linux-arm64` |

```bash
chmod +x segura-*
mv segura-* ~/.local/bin/segura
```

---

## Configuration

Set these environment variables (in `~/.zshrc`, `~/.bashrc`, or a `.env` file):

| Variable | Description | Required |
|----------|-------------|----------|
| `SEGURA_URL` | senhasegura instance URL | Yes |
| `SEGURA_USER` | Username | Yes |
| `SEGURA_PASSWORD` | Password | Yes |
| `SEGURA_MFA_TOKEN` | TOTP secret (base32) | Yes |
| `SEGURA_TENANT` | Tenant name (auto-detected from URL if omitted) | No |

```bash
export SEGURA_URL="https://your-instance.senhasegura.app/"
export SEGURA_USER="your-username"
export SEGURA_PASSWORD="your-password"
export SEGURA_MFA_TOKEN="YOUR_TOTP_SECRET"
export SEGURA_TENANT="your-tenant"
```

After setting the variables, reload your shell:

```bash
source ~/.zshrc   # or source ~/.bashrc
```

You can also use `--env /path/to/.env` to load from a file.

---

## Commands

### List credentials

```bash
segura list
```

```
Available credentials (3):

  Credential           Device                         Command
  ----------           ------                         -------
  root                 srv-web (10.0.4.52)            segura connect root@10.0.4.52
  admin                srv-db (10.0.4.53)             segura connect admin@10.0.4.53
  deploy               srv-app (10.0.4.54)            segura connect deploy@10.0.4.54
```

### SSH Terminal (default)

```bash
segura connect root@10.0.4.52
```

Direct SSH connection through the senhasegura gateway. TOTP and password are sent automatically. Press `Ctrl+]` to disconnect.

```bash
# Custom SSH port
segura connect root@10.0.4.52 --port 2222
```

### Interactive picker, recents & favorites

Run `connect` with **no argument** to open a fuzzy picker of all your
credentials — type to filter by user/device/IP, `Enter` to connect. Favorites
(★) and recently-used (•) targets are listed first.

```bash
segura connect            # interactive picker
segura connect web        # picker pre-filtered by "web"

segura recent             # pick from recent/favorite targets to reconnect
segura recent --list      # just print them

segura fav add root@10.0.4.52   # pin a favorite
segura fav                       # list favorites
segura fav rm root@10.0.4.52     # unpin
```

Recents/favorites are stored in `~/.segura/recent.json`.

### Browser Terminal (multi-tab)

```bash
segura connect root@10.0.4.52 --browser
```

Opens a local **multi-tab web console** in your default browser (auto-starts the
web server daemon if needed). Use the **＋** button (or `Ctrl/Cmd+T`) to open more
servers as tabs in the same window; each tab is an isolated session and closing a
tab tears down its connection.

### Interactive SFTP browser

```bash
segura sftp root@10.0.4.52
```

A terminal file browser over SFTP — no web app needed. Commands inside the prompt:

```
ls [path]   ll [path]   cd <path>   pwd
get <remote> [local]    put <local> [remote]    cat <file>
mkdir <path>   rm <path>   mv <old> <new>
lcd <dir>   lpwd   lls        help   exit
```

`put`/`mkdir`/`rm`/`mv` retry with sudo automatically on permission-denied. Run a
single command non-interactively with `-c`:

```bash
segura sftp root@10.0.4.52 -c "get /etc/hosts ./hosts"
```

### Port forwarding

Forward a local port to a service reachable from the device (databases, admin
UIs, any TCP service on the device's loopback), tunneled through the gateway:

```bash
# Expose the device's MySQL/MariaDB locally on 3306
segura forward -L 3306:127.0.0.1:3306 dbuser@SRV-DB1

# Local 8080 -> the device's web admin on 80
segura forward -L 8080:127.0.0.1:80 admin@SRV-WEB1
```

Runs until `Ctrl-C`. The tunnel is scoped to the device's own network, so target
`127.0.0.1` to reach services bound there.

### Native ssh / scp / rsync (ssh-config)

Generate `~/.ssh/config` entries that route native `ssh`/`scp`/`rsync`/`git`/VS
Code Remote to your devices through the gateway:

```bash
segura ssh-config           # print the entries
segura ssh-config --write   # append/update them in ~/.ssh/config

# then, natively:
ssh segura-srv-web-root
scp file.txt segura-srv-web-root:/tmp/
```

> **Note:** the tunnel gives native `ssh` a raw connection to the device's SSH
> server — you then authenticate to the **device** with your own key or password.
> segura injects the vault credential for its *own* sessions (`connect`, `sftp`),
> not for the native client, so this is most useful where you have key-based
> access to devices.

### SCP File Transfer

```bash
# Local file to remote
segura copy localfile.txt root@10.0.4.52:/tmp/

# Remote file to local
segura copy root@10.0.4.52:/tmp/remotefile.txt ./

# Local directory to remote (recursion auto-enabled)
segura copy ./deploy/ root@10.0.4.52:/tmp/deploy/

# Remote directory to local (use -r for downloads)
segura copy -r root@10.0.4.52:/etc/app ./app-backup/
```

Directories are copied recursively — automatically when the source is a local
directory, or with `-r`/`--recursive` when downloading a remote directory.

Requires `sshpass` (`brew install hudochenkov/sshpass/sshpass` on macOS).

### Auto-login

```bash
segura login
```

Opens senhasegura in Chrome with credentials and TOTP auto-filled.

### Shell completion

```bash
segura install-completion
```

Installs tab-completion for your shell (auto-detects **zsh**, **bash**, or **fish**)
and wires it into your shell RC. It's also run automatically during install and on
every `segura update`, so completion stays in sync with new commands and flags.
Restart your shell (or `source` your RC) to activate it. To generate a script
manually instead, use `segura completion <shell>`.

### Update

```bash
segura update
```

Checks GitHub for a newer version and replaces the binary in-place.

### Uninstall

```bash
segura uninstall
```

Removes the binary, `~/.segura/` data, and SEGURA env vars from shell RC files. Use `-y` to skip confirmation.

### Version

```bash
segura version
```

---

## Web Terminal Server

### Start as daemon

```bash
segura --start --port 8080
segura --stop
segura --restart --port 8080
```

PID file: `~/.segura/segura.pid` | Logs: `~/.segura/segura.log`

### Start in foreground

```bash
segura web --port 8080
```

Then open `http://localhost:8080` in your browser.

---

## Screenshots

### Dashboard

The dashboard shows all available PAM credentials. Click one to open a multi-tab terminal console.

![Dashboard](docs/screenshots/dashboard.gif)

### Web Terminal

Full Guacamole-based terminal with native copy/paste support (Ctrl+C/V).

![Terminal](docs/screenshots/terminal.gif)

### SFTP File Manager

Browse, upload, download, and edit remote files. Supports sudo for permission-restricted operations.

![File Manager](docs/screenshots/file-manager.gif)

---

## Web Terminal Features

### Multi-tab console
- Multiple server sessions as tabs in one browser window
- **＋** button or **Ctrl/Cmd+T** to open another server (fuzzy picker)
- Each tab is an isolated session; closing a tab tears down its connection

### Terminal
- Full Guacamole terminal rendered in the browser
- **Cmd+C / Cmd+V** (macOS) and **Ctrl+Shift+C / Ctrl+Shift+V** (Linux/Windows) — native copy/paste; plain **Ctrl+C** still sends SIGINT to the remote
- Right-click menu (Copy / Paste) + a paste fallback that works on every browser
- **Ctrl+A** — select all works in browser

### SFTP File Manager
- **Browse** — navigate directories with breadcrumb navigation
- **Upload** — button or drag & drop (files and folders)
- **Download** — select files with checkboxes, click Download
- **View** — double-click any non-binary file
- **Edit** — modify files in-browser, save with one click
- **Sudo** — automatic retry with sudo on permission denied

---

## Build from source

Requires Go 1.25+ (see `go.mod`).

```bash
git clone https://github.com/papesambandour/segura-cli.git
cd segura-cli

# Build for current platform
make build

# Cross-compile all platforms
make release

# Install locally (copies to /usr/local/bin, env vars in .zshrc, shell completion)
make install
```

See [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) for the full release workflow (CI, versioning, cross-compilation).

---

## Troubleshooting

| Problem | Solution |
|---------|----------|
| `SEGURA_URL is required` | Env vars not loaded. Run `source ~/.zshrc` or use `--env .env` |
| `no PID file found` | No daemon running. Start with `segura --start` |
| `already running` | Use `--stop` or `--restart` |
| `Too many authentication failures` | Check `SEGURA_TENANT` matches your hostname |
| Connection timeout in browser | Auth takes 10-15s on first request. Refresh the page |
| Permission denied (file upload/save) | Retry with sudo when prompted |
| SFTP handshake EOF | SSH Terminal Proxy may not be enabled for this credential |
| Copy/paste not working in web terminal | Use Cmd+C/V (macOS) or Ctrl+Shift+C/V (Linux/Win); or right-click → Paste (fallback works on every browser) |
| `segura forward` connects but the service is unreachable | Target the device loopback (`127.0.0.1:<port>`) — the tunnel is scoped to the device's own network |
| Native `ssh` via `ssh-config` says "Permission denied" | Expected unless you have your own key/password on the device; segura only injects the vault credential for `connect`/`sftp` |
