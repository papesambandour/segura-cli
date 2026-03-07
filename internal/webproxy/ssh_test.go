package webproxy

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/ssh"
)

func TestSSHDirect(t *testing.T) {
	cfg := testConfig(t)
	host := cfg.Host + ":22"
	password := cfg.Password
	code, _ := totp.GenerateCode(cfg.MFAToken, time.Now())
	t.Logf("TOTP: %s", code)

	tests := []struct {
		name     string
		user     string
		password string
	}{
		{"totp_in_user+vault_pass", fmt.Sprintf("%s[psndour@10.0.4.52]%s", cfg.User, code), password},
		{"no_totp+vault_pass", fmt.Sprintf("%s[psndour@10.0.4.52]", cfg.User), password},
		{"totp_in_user+totp_pass", fmt.Sprintf("%s[psndour@10.0.4.52]%s", cfg.User, code), code},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Regenerate TOTP for each attempt
			code, _ = totp.GenerateCode(cfg.MFAToken, time.Now())
			user := tt.user
			if fmt.Sprintf("%s", tt.user) != tt.user { // re-gen for totp cases
				user = tt.user
			}

			t.Logf("User: %s", user)
			t.Logf("Pass: %s", tt.password)

			// Try password auth
			config := &ssh.ClientConfig{
				User: user,
				Auth: []ssh.AuthMethod{
					ssh.Password(tt.password),
				},
				HostKeyCallback: ssh.InsecureIgnoreHostKey(),
				Timeout:         10 * time.Second,
			}

			client, err := ssh.Dial("tcp", host, config)
			if err != nil {
				t.Logf("Password auth error: %v", err)
			} else {
				t.Log("PASSWORD AUTH SUCCESS!")
				client.Close()
				return
			}

			// Try keyboard-interactive
			code2, _ := totp.GenerateCode(cfg.MFAToken, time.Now())
			user2 := fmt.Sprintf("%s[psndour@10.0.4.52]%s", cfg.User, code2)

			config2 := &ssh.ClientConfig{
				User: user2,
				Auth: []ssh.AuthMethod{
					ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) (answers []string, err error) {
						t.Logf("  KBI user=%q instruction=%q", user, instruction)
						answers = make([]string, len(questions))
						for i, q := range questions {
							t.Logf("  KBI q[%d]=%q echo=%v", i, q, echos[i])
							answers[i] = tt.password
						}
						if len(questions) == 0 {
							t.Log("  KBI: no questions asked!")
						}
						return answers, nil
					}),
				},
				HostKeyCallback: ssh.InsecureIgnoreHostKey(),
				Timeout:         10 * time.Second,
			}

			client2, err := ssh.Dial("tcp", host, config2)
			if err != nil {
				t.Logf("KBI auth error: %v", err)
			} else {
				t.Log("KBI AUTH SUCCESS!")
				client2.Close()
			}
		})
	}
}

func TestSSHPasswordAuth(t *testing.T) {
	cfg := testConfig(t)
	host := cfg.Host + ":22"
	password := cfg.Password
	code, _ := totp.GenerateCode(cfg.MFAToken, time.Now())

	// Try with both password and keyboard-interactive simultaneously
	user := fmt.Sprintf("%s[psndour@10.0.4.52]%s", cfg.User, code)
	t.Logf("User: %s", user)

	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
			ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) (answers []string, err error) {
				fmt.Fprintf(os.Stderr, "KBI callback: user=%q instruction=%q questions=%v\n", user, instruction, questions)
				answers = make([]string, len(questions))
				for i := range questions {
					answers[i] = password
				}
				return answers, nil
			}),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}

	client, err := ssh.Dial("tcp", host, config)
	if err != nil {
		t.Logf("Connection error: %v", err)

		// Try with just KBI and see details
		code2, _ := totp.GenerateCode(cfg.MFAToken, time.Now())
		user2 := fmt.Sprintf("%s[psndour@10.0.4.52]%s", cfg.User, code2)

		t.Logf("\nTrying KBI-only with user: %s", user2)
		config2 := &ssh.ClientConfig{
			User: user2,
			Auth: []ssh.AuthMethod{
				ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) (answers []string, err error) {
					t.Logf("KBI: instruction=%q, %d questions", instruction, len(questions))
					for i, q := range questions {
						t.Logf("  Q[%d]=%q echo=%v", i, q, echos[i])
					}
					answers = make([]string, len(questions))
					for i := range questions {
						answers[i] = password
					}
					return answers, nil
				}),
			},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         15 * time.Second,
		}

		client2, err := ssh.Dial("tcp", host, config2)
		if err != nil {
			t.Logf("KBI-only error: %v", err)
		} else {
			t.Log("KBI-only SUCCESS!")
			client2.Close()
		}
	} else {
		t.Log("SUCCESS!")
		client.Close()
	}
}
