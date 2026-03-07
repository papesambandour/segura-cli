package webproxy

import (
	"regexp"
	"strings"
)

// extractCSRFToken finds the CSRF token from an HTML page (legacy format).
func extractCSRFToken(html string) string {
	re := regexp.MustCompile(`name="_token"\s+value="([^"]+)"`)
	if m := re.FindStringSubmatch(html); len(m) > 1 {
		return m[1]
	}
	re = regexp.MustCompile(`<meta\s+name="csrf-token"\s+content="([^"]+)"`)
	if m := re.FindStringSubmatch(html); len(m) > 1 {
		return m[1]
	}
	return ""
}

// extractFormAction finds the form action URL from HTML.
func extractFormAction(html string) string {
	re := regexp.MustCompile(`<form[^>]*action="([^"]+)"`)
	if m := re.FindStringSubmatch(html); len(m) > 1 {
		return m[1]
	}
	return ""
}

// Credential represents a senhasegura credential that can be accessed.
type Credential struct {
	Username string `json:"username"`
	Device   string `json:"device"`
	IP       string `json:"ip"`
	SRToken  string `json:"-"` // Never exposed to frontend
}

// ParseCredentials extracts credentials from the senhasegura dashboard HTML.
// The dashboard uses a card-based layout where each credential card contains:
//   - Device: <div class="small ss-color-primary">DEVICE_NAME (IP)</div>
//   - Username: <div class="fw-bold ss-color-primary ...">username</div>
//   - Action link: <a href="/flow/coac/action/session/default?_sr=TOKEN"...>
func ParseCredentials(html string) []Credential {
	var credentials []Credential

	// Find all session action links
	linkRe := regexp.MustCompile(`/flow/coac/action/session/default\?_sr=([^"&]+)`)
	linkMatches := linkRe.FindAllStringSubmatchIndex(html, -1)

	// For device+IP: <div class="small ss-color-primary">DEVICE (IP)</div>
	deviceRe := regexp.MustCompile(`<div[^>]*class="[^"]*small[^"]*ss-color-primary[^"]*"[^>]*>([^<]+)</div>`)

	// For username: <div class="fw-bold ss-color-primary ...">username</div>
	usernameRe := regexp.MustCompile(`<div[^>]*class="[^"]*fw-bold[^"]*ss-color-primary[^"]*"[^>]*>([^<]+)</div>`)

	// IP in parentheses pattern
	ipRe := regexp.MustCompile(`\((\d+\.\d+\.\d+\.\d+)\)`)

	for _, loc := range linkMatches {
		srToken := html[loc[2]:loc[3]]

		// Search backwards for device and username info (within 2000 chars before the link)
		searchStart := loc[0] - 2000
		if searchStart < 0 {
			searchStart = 0
		}
		context := html[searchStart:loc[0]]

		cred := Credential{
			SRToken: srToken,
		}

		// Find device info (last match before the link)
		deviceMatches := deviceRe.FindAllStringSubmatch(context, -1)
		if len(deviceMatches) > 0 {
			lastDevice := deviceMatches[len(deviceMatches)-1][1]
			lastDevice = strings.TrimSpace(lastDevice)

			// Extract IP from "DEVICE_NAME (IP)" format
			if ipMatch := ipRe.FindStringSubmatch(lastDevice); len(ipMatch) > 1 {
				cred.IP = ipMatch[1]
				// Device name is everything before the IP part
				cred.Device = strings.TrimSpace(strings.Split(lastDevice, "(")[0])
			} else {
				cred.Device = lastDevice
			}
		}

		// Find username (last match before the link)
		usernameMatches := usernameRe.FindAllStringSubmatch(context, -1)
		if len(usernameMatches) > 0 {
			cred.Username = strings.TrimSpace(usernameMatches[len(usernameMatches)-1][1])
		}

		if cred.Username != "" && (cred.IP != "" || cred.Device != "") {
			// Check for duplicates
			isDup := false
			for _, existing := range credentials {
				if existing.Username == cred.Username && existing.IP == cred.IP {
					isDup = true
					break
				}
			}
			if !isDup {
				credentials = append(credentials, cred)
			}
		}
	}

	return credentials
}

// FindCredential finds a credential matching the given username and device/IP.
func FindCredential(credentials []Credential, username, deviceOrIP string) *Credential {
	for i, cred := range credentials {
		if cred.Username == username && (cred.IP == deviceOrIP || cred.Device == deviceOrIP) {
			return &credentials[i]
		}
	}
	return nil
}
