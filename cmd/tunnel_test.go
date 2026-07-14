package cmd

import (
	"os"
	"strings"
	"testing"

	"segura-cli/internal/webproxy"
)

func TestParseForwardSpec(t *testing.T) {
	cases := []struct {
		in                       string
		bind, lport, remote      string
		wantErr                  bool
	}{
		{"3306:127.0.0.1:3306", "127.0.0.1", "3306", "127.0.0.1:3306", false},
		{"8080:127.0.0.1:80", "127.0.0.1", "8080", "127.0.0.1:80", false},
		{"0.0.0.0:5432:127.0.0.1:5432", "0.0.0.0", "5432", "127.0.0.1:5432", false},
		{"bad", "", "", "", true},
		{"1:2", "", "", "", true},
		{"1:2:3:4:5", "", "", "", true},
	}
	for _, c := range cases {
		bind, lport, remote, err := parseForwardSpec(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseForwardSpec(%q): expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseForwardSpec(%q): unexpected error %v", c.in, err)
			continue
		}
		if bind != c.bind || lport != c.lport || remote != c.remote {
			t.Errorf("parseForwardSpec(%q) = (%q,%q,%q), want (%q,%q,%q)",
				c.in, bind, lport, remote, c.bind, c.lport, c.remote)
		}
	}
}

func TestNormalizeHostPort(t *testing.T) {
	if got := normalizeHostPort("22"); got != "127.0.0.1:22" {
		t.Errorf("normalizeHostPort(22) = %q", got)
	}
	if got := normalizeHostPort("10.0.0.5:80"); got != "10.0.0.5:80" {
		t.Errorf("normalizeHostPort(10.0.0.5:80) = %q", got)
	}
}

func TestSanitizeAliasAndHostAlias(t *testing.T) {
	if got := sanitizeAlias("SRV-DB1.prod"); got != "srv-db1-prod" {
		t.Errorf("sanitizeAlias = %q", got)
	}
	if got := sanitizeAlias("--weird@name!!"); got != "weird-name" {
		t.Errorf("sanitizeAlias weird = %q", got)
	}
	a := hostAlias(webproxy.Credential{Username: "root", Device: "SRV-WEB 01", IP: "10.0.0.1"})
	if a != "segura-srv-web-01-root" {
		t.Errorf("hostAlias = %q", a)
	}
	// falls back to IP when device empty
	a2 := hostAlias(webproxy.Credential{Username: "u", Device: "", IP: "10.0.0.9"})
	if a2 != "segura-10-0-0-9-u" {
		t.Errorf("hostAlias fallback = %q", a2)
	}
}

func TestRenderSSHConfigIdempotentAndValid(t *testing.T) {
	creds := []webproxy.Credential{
		{Username: "root", Device: "web01", IP: "10.0.0.1"},
		{Username: "admin", Device: "db02", IP: "10.0.0.2"},
	}
	out := renderSSHConfig(creds, "/usr/local/bin/segura")
	for _, want := range []string{
		sshConfigTag,
		"Host segura-db02-admin",
		"Host segura-web01-root",
		"HostName 127.0.0.1",
		`ProxyCommand "/usr/local/bin/segura" proxy root@web01`,
		"UserKnownHostsFile /dev/null",
		sshConfigTag + " end",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered config missing %q\n---\n%s", want, out)
		}
	}
}

func TestReplaceTaggedBlock(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/config"
	// pre-existing content + an old block
	initial := "Host existing\n    HostName 1.2.3.4\n\n" + sshConfigTag + "\nHost old\n" + sshConfigTag + " end\n"
	if err := os.WriteFile(p, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	block := sshConfigTag + "\nHost new\n" + sshConfigTag + " end\n"
	if err := replaceTaggedBlock(p, sshConfigTag, block); err != nil {
		t.Fatal(err)
	}
	gotB, _ := os.ReadFile(p)
	got := string(gotB)
	if !strings.Contains(got, "Host existing") {
		t.Errorf("pre-existing content lost:\n%s", got)
	}
	if strings.Contains(got, "Host old") {
		t.Errorf("old block not replaced:\n%s", got)
	}
	if !strings.Contains(got, "Host new") {
		t.Errorf("new block not written:\n%s", got)
	}
	if strings.Count(got, sshConfigTag+"\n") != 1 {
		t.Errorf("tag duplicated:\n%s", got)
	}
}
