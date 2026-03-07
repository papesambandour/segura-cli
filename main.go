package main

import "segura-cli/cmd"

var (
	Version    = "dev"
	Commit     = "none"
	BuildDate  = "unknown"
	GithubRepo = "papesambandour/segura-cli"
)

func main() {
	cmd.SetVersionInfo(Version, Commit, BuildDate, GithubRepo)
	cmd.Execute()
}
