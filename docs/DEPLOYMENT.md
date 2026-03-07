# SEGURA-CLI — Guide de deploiement

## Table des matieres

1. [Vue d'ensemble](#vue-densemble)
2. [Prerequis](#prerequis)
3. [Configuration initiale](#configuration-initiale)
4. [Build local](#build-local)
5. [Cross-compilation (release)](#cross-compilation-release)
6. [Publier une release GitHub](#publier-une-release-github)
7. [Installation pour les utilisateurs](#installation-pour-les-utilisateurs)
8. [Mise a jour (segura update)](#mise-a-jour)
9. [Desinstallation (segura uninstall)](#desinstallation)
10. [Reference des commandes](#reference-des-commandes)

---

## Vue d'ensemble

Le cycle de deploiement de SEGURA-CLI fonctionne ainsi :

```
Developpeur                          Utilisateur
───────────                          ───────────
make deploy  (ou deploy-minor / deploy-major)
       │  ← verifie qu'il n'y a pas de changements non commites
       │  ← push la branche release
       │  ← cree le tag et le pousse
       ▼
GitHub Actions (release.yml)
  - cross-compile 4 binaires
  - cree une Release GitHub
       │
       ▼
GitHub Releases
  segura-darwin-arm64                curl | bash ──► install.sh
  segura-darwin-amd64                                  │
  segura-linux-arm64                                   ▼
  segura-linux-amd64               ~/.local/bin/segura
  checksums.txt
                                   segura update  ──► telecharge la derniere version
                                   segura uninstall ► supprime tout
```

**Plateformes supportees :**


| OS    | Architecture             | Binaire               |
| ----- | ------------------------ | --------------------- |
| macOS | Apple Silicon (M1/M2/M3) | `segura-darwin-arm64` |
| macOS | Intel                    | `segura-darwin-amd64` |
| Linux | ARM 64-bit               | `segura-linux-arm64`  |
| Linux | x86 64-bit               | `segura-linux-amd64`  |

---

## Prerequis

### Pour le developpeur (build)

- Go 1.21+ installe
- Git
- `make`

### Pour l'utilisateur (installation)

- `curl` ou `wget`
- Aucun autre outil requis (binaire statique, zero dependance)

---

## Configuration initiale

### 1. Definir le repo GitHub

Avant de publier, mettre a jour le nom du repo dans **3 fichiers** :

**`Makefile`** (ligne 11) :

```makefile
GITHUB_REPO ?= papesambandour/segura-cli
```

**`install.sh`** (ligne 5) :

```bash
REPO="papesambandour/segura-cli"
```

**`main.go`** (ligne 10) — valeur par defaut compilee dans le binaire :

```go
GithubRepo = "papesambandour/segura-cli"
```

> Ces 3 valeurs doivent etre identiques. Le Makefile injecte `GITHUB_REPO` dans le binaire via ldflags, donc c'est lui qui fait foi au build.

### 2. Ajouter le remote Git

```bash
git remote add origin git@github.com:papesambandour/segura-cli.git
git push -u origin main
```

---

## Build local

Compile le binaire pour la machine actuelle :

```bash
make build
```

Ce que ca fait :

1. `CGO_ENABLED=0` — binaire 100% statique, pas de dependance libc
2. Injecte la version (tag git), le commit, la date de build et le repo GitHub dans le binaire via `-ldflags`
3. Produit `./segura` dans le dossier courant

Verifier :

```bash
./segura version
# segura v1.0.0
#   commit:  a1b2c3d
#   built:   2026-03-06T12:00:00Z
```

### Install local (dev machine)

```bash
make install
```

Ce que ca fait :

1. Build le binaire
2. Copie vers `/usr/local/bin/segura` (demande sudo)
3. Lit le fichier `.env` et injecte les variables dans `~/.zshrc` sous un bloc `# SEGURA-CLI env`

Desinstaller (dev machine) :

```bash
make uninstall
```

Supprime le binaire de `/usr/local/bin` et le bloc env de `~/.zshrc`.

---

## Cross-compilation (release)

Genere les 4 binaires pour toutes les plateformes :

```bash
make release
```

Resultat dans `dist/` :

```
dist/
  segura-darwin-arm64    # macOS Apple Silicon
  segura-darwin-amd64    # macOS Intel
  segura-linux-arm64     # Linux ARM
  segura-linux-amd64     # Linux x86
```

Avec checksums SHA-256 :

```bash
make release-checksums
# dist/checksums.txt contient les hash de chaque binaire
```

Nettoyer :

```bash
make clean
# Supprime ./segura et dist/
```

---

## Publier une release GitHub

### Methode automatique (recommandee) — `make deploy`

Les commandes `make deploy` automatisent tout le processus : verification, push de la branche, creation du tag et declenchement de GitHub Actions.

```bash
# 1. Commiter ses changements
git add -A && git commit -m "feat: nouvelle fonctionnalite"

# 2. Deployer (choisir le niveau de version)
make deploy          # patch : v1.0.0 -> v1.0.1
make deploy-minor    # minor : v1.0.0 -> v1.1.0
make deploy-major    # major : v1.0.0 -> v2.0.0
```

Ce que `make deploy` fait automatiquement :


| Etape | Action                                                             |
| ----- | ------------------------------------------------------------------ |
| 1     | Verifie qu'il n'y a**aucun changement non commite** (sinon erreur) |
| 2     | Pousse la branche`release` vers GitHub                             |
| 3     | Calcule le nouveau tag (increment automatique)                     |
| 4     | Cree le tag Git et le pousse                                       |
| 5     | GitHub Actions se declenche et cree la release                     |

Si des fichiers ne sont pas commites :

```
$ make deploy
Error: you have uncommitted changes. Commit or stash them first.
 M Makefile
 M cmd/root.go
```

### Convention de versioning

Format : `vMAJEUR.MINEUR.PATCH` (semver)


| Commande            | Exemple          | Quand                                     |
| ------------------- | ---------------- | ----------------------------------------- |
| `make deploy`       | v1.0.0 -> v1.0.1 | Bug fix, correction mineure               |
| `make deploy-minor` | v1.0.0 -> v1.1.0 | Nouvelle fonctionnalite, retro-compatible |
| `make deploy-major` | v1.0.0 -> v2.0.0 | Changement cassant (breaking change)      |

### Methode manuelle

Si GitHub Actions n'est pas configure :

```bash
# 1. Build les binaires
make release-checksums

# 2. Creer la release manuellement sur GitHub
# Aller sur https://github.com/papesambandour/segura-cli/releases/new
# Tag: v1.0.0
# Uploader les fichiers de dist/
```

---

## Installation pour les utilisateurs

### Methode 1 : curl | bash (recommandee)

L'utilisateur execute une seule commande :

```bash
curl -sSL https://papesambandour.github.io/segura-cli/install.sh | bash
```

Ce que le script fait :


| Etape | Action                                                              |
| ----- | ------------------------------------------------------------------- |
| 1     | Detecte l'OS (`darwin`/`linux`) et l'architecture (`arm64`/`amd64`) |
| 2     | Interroge l'API GitHub pour trouver la derniere release             |
| 3     | Telecharge le binaire correspondant depuis GitHub Releases          |
| 4     | Installe dans`~/.local/bin/segura` (sans sudo)                      |
| 5     | Ajoute`~/.local/bin` au PATH dans `.zshrc` ou `.bashrc`             |
| 6     | Affiche les variables d'environnement a configurer                  |

En **mode pipe** (`curl | bash`), le script saute les prompts interactifs et affiche juste les variables a configurer manuellement.

### Methode 2 : Installation interactive

```bash
# Telecharger le script
curl -O https://papesambandour.github.io/segura-cli/install.sh

# Executer en mode interactif
bash install.sh
```

En mode interactif, le script pose des questions pour configurer automatiquement les variables d'environnement :

```
SEGURA_URL (senhasegura host URL): https://pam.example.com
SEGURA_USER (senhasegura username): your-username
SEGURA_PASSWORD (senhasegura password): ********
SEGURA_MFA_TOKEN (TOTP secret key): ***********
SEGURA_TENANT (senhasegura tenant name): your-tenant
```

Les valeurs sont ecrites comme `export` dans le fichier RC du shell.

### Methode 3 : Telechargement direct

```bash
# Exemple pour macOS Apple Silicon
curl -fSL -o segura \
  https://github.com/papesambandour/segura-cli/releases/latest/download/segura-darwin-arm64
chmod +x segura
sudo mv segura /usr/local/bin/
```

### Apres l'installation

```bash
# Recharger le shell
source ~/.zshrc   # ou source ~/.bashrc

# Verifier l'installation
segura version

# Tester
segura list
```

---

## Mise a jour

```bash
segura update
```

Ce que ca fait :

```
1. Affiche la version actuelle (compilee dans le binaire)
2. Interroge GitHub API : GET /repos/{owner}/{repo}/releases/latest
3. Compare la version actuelle avec la derniere release
   - Compare semver (v1.0.0 vs v1.1.0)
   - Si la version actuelle est "dev" ou un hash de commit, toute release est consideree plus recente
4. Si une mise a jour est disponible :
   a. Telecharge le binaire pour l'OS/arch actuel (runtime.GOOS/runtime.GOARCH)
   b. Ecrit un fichier temporaire a cote du binaire actuel (.new)
   c. Renomme atomiquement le .new sur l'ancien binaire
5. Si deja a jour : affiche "You are already on the latest version."
```

Exemple de sortie :

```
$ segura update
Current version: v1.0.0
Latest version:  v1.1.0

New version available: v1.0.0 -> v1.1.0
Downloading https://github.com/.../segura-darwin-arm64...
Updated segura to v1.1.0
```

> **Note :** Si le binaire est installe dans un repertoire protege (ex: `/usr/local/bin`), il faudra peut-etre lancer `sudo segura update`.

---

## Desinstallation

```bash
segura uninstall
```

Le binaire se supprime lui-meme. Voici ce qui est fait :


| Etape | Action                                                                     |
| ----- | -------------------------------------------------------------------------- |
| 1     | Affiche ce qui va etre supprime et demande confirmation (`Proceed? [y/N]`) |
| 2     | Arrete le daemon s'il tourne (equivalent de`segura --stop`)                |
| 3     | Supprime`~/.segura/` (PID file, logs)                                      |
| 4     | Nettoie les variables`SEGURA_*` dans `~/.zshrc` et `~/.bashrc`             |
| 5     | Supprime le binaire lui-meme                                               |

Pour sauter la confirmation :

```bash
segura uninstall -y
```

Ce qui est nettoye dans les fichiers RC :

- Le bloc `# SEGURA-CLI env` ... `# SEGURA-CLI env end` (installe par `make install`)
- Les lignes `export SEGURA_URL=...`, `export SEGURA_USER=...`, etc. (installees par `install.sh`)
- Le commentaire `# Added by segura installer`

---

## Reference des commandes

### Commandes CLI


| Commande              | Description                              |
| --------------------- | ---------------------------------------- |
| `segura version`      | Affiche version, commit et date de build |
| `segura --version`    | Affiche la version (format court)        |
| `segura update`       | Verifie et installe la derniere version  |
| `segura uninstall`    | Supprime segura completement             |
| `segura uninstall -y` | Supprime sans confirmation               |

### Targets Makefile (pour le developpeur)


| Target                   | Description                                                 |
| ------------------------ | ----------------------------------------------------------- |
| `make build`             | Build local (machine actuelle, binaire statique)            |
| `make release`           | Cross-compile les 4 binaires dans`dist/`                    |
| `make release-checksums` | `release` + fichier `checksums.txt`                         |
| `make install`           | Build + copie dans`/usr/local/bin` + env vars dans `.zshrc` |
| `make uninstall`         | Supprime de`/usr/local/bin` + nettoie `.zshrc`              |
| `make clean`             | Supprime`./segura` et `dist/`                               |
| `make deploy`            | Verifie, push branche, tag patch, push tag                  |
| `make deploy-minor`      | Idem avec increment mineur                                  |
| `make deploy-major`      | Idem avec increment majeur                                  |

### Variables Makefile


| Variable      | Default                      | Description                       |
| ------------- | ---------------------------- | --------------------------------- |
| `VERSION`     | `git describe --tags`        | Version injectee dans le binaire  |
| `COMMIT`      | `git rev-parse --short HEAD` | Hash du commit                    |
| `GITHUB_REPO` | `papesambandour/segura-cli`         | Repo GitHub (pour`segura update`) |

Override possible :

```bash
make build VERSION=v2.0.0 GITHUB_REPO=myorg/myrepo
```

### Variables d'environnement (utilisateur)


| Variable           | Description                |
| ------------------ | -------------------------- |
| `SEGURA_URL`       | URL du serveur senhasegura |
| `SEGURA_USER`      | Nom d'utilisateur          |
| `SEGURA_PASSWORD`  | Mot de passe               |
| `SEGURA_MFA_TOKEN` | Cle secrete TOTP (base32)  |
| `SEGURA_TENANT`    | Nom du tenant              |

---

## Workflow complet : de dev a utilisateur

```bash
# ── Developpeur ──────────────────────────────────

# 1. Developper et tester
make build
./segura version

# 2. Commiter
git add -A
git commit -m "feat: nouvelle fonctionnalite"

# 3. Deployer (push branche + tag automatique)
make deploy          # patch
# ou make deploy-minor / make deploy-major

# 4. GitHub Actions cree la release automatiquement

# ── Utilisateur ──────────────────────────────────

# 4. Premiere installation
curl -sSL https://papesambandour.github.io/segura-cli/install.sh | bash
source ~/.zshrc

# 5. Utilisation quotidienne
segura list
segura connect user@device

# 6. Mise a jour
segura update

# 7. Desinstallation
segura uninstall
```

---

## Fichiers impliques

```
.
├── main.go                          # Variables Version/Commit/BuildDate/GithubRepo (ldflags)
├── cmd/
│   ├── root.go                      # SetVersionInfo(), commande version, --version
│   ├── update.go                    # segura update (self-update via GitHub API)
│   └── uninstall.go                 # segura uninstall (auto-suppression)
├── Makefile                         # build, release, install, uninstall, clean, deploy
├── install.sh                       # Script d'installation curl | bash
└── .github/
    └── workflows/
        └── release.yml              # CI : cross-compile + GitHub Release sur tag v*
```
