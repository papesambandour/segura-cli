package webproxy

import (
	"encoding/json"
	"io"
	"net/url"
	"strings"
	"testing"
)

func TestGuacamoleGetCredentials(t *testing.T) {
	cfg := testConfig(t)

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := client.Login(); err != nil {
		t.Fatalf("Login: %v", err)
	}

	// Get proxy URL + OAS token
	dashHTML, err := client.GetPage("/flow/coge/desktop/dashboard")
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	creds := ParseCredentials(dashHTML)
	if len(creds) == 0 {
		t.Fatal("No credentials")
	}

	proxyURL, err := client.GetProxyURL(creds[0].SRToken)
	if err != nil {
		t.Fatalf("GetProxyURL: %v", err)
	}

	// Extract OAS token
	parts := strings.Split(proxyURL, "/client/")
	tokenParts := strings.SplitN(strings.TrimRight(parts[1], "/,"), "/", 2)
	oasToken := strings.TrimRight(tokenParts[1], ",")

	// Exchange OAS token for Guacamole auth token
	resp, err := client.http.PostForm(client.baseURL+"/proxy/api/tokens", url.Values{"token": {oasToken}})
	if err != nil {
		t.Fatalf("Token exchange: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var tokenResp map[string]interface{}
	json.Unmarshal(body, &tokenResp)
	authToken := tokenResp["authToken"].(string)
	t.Logf("Auth token: %s...", authToken[:30])

	// Use "default" data source (from the token response)
	dataSource := "default"

	// List connections
	connURL := client.baseURL + "/proxy/api/session/data/" + dataSource + "/connections?token=" + authToken
	resp, err = client.http.Get(connURL)
	if err != nil {
		t.Fatalf("Connections: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	t.Logf("Connections status: %d", resp.StatusCode)

	if resp.StatusCode == 200 {
		var connections map[string]interface{}
		json.Unmarshal(body, &connections)
		t.Logf("Found %d connections", len(connections))

		for connID, connData := range connections {
			t.Logf("\nConnection: %s", connID)
			m, ok := connData.(map[string]interface{})
			if !ok {
				continue
			}
			for k, v := range m {
				t.Logf("  %s: %v", k, v)
			}

			// Get connection parameters (might contain SSH password!)
			paramURL := client.baseURL + "/proxy/api/session/data/" + dataSource + "/connections/" + connID + "/parameters?token=" + authToken
			resp2, err := client.http.Get(paramURL)
			if err != nil {
				t.Logf("  Params error: %v", err)
				continue
			}
			body2, _ := io.ReadAll(resp2.Body)
			resp2.Body.Close()
			t.Logf("  Params status: %d", resp2.StatusCode)
			t.Logf("  Params: %s", string(body2))
		}
	} else {
		t.Logf("Response: %s", string(body))
	}

	// Also check active connections
	activeURL := client.baseURL + "/proxy/api/session/data/" + dataSource + "/activeConnections?token=" + authToken
	resp, err = client.http.Get(activeURL)
	if err != nil {
		t.Logf("Active connections error: %v", err)
	} else {
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Logf("\nActive connections status: %d", resp.StatusCode)
		if len(body) < 3000 {
			t.Logf("Active: %s", string(body))
		}
	}

	// Check the session/tunnel info
	selfURL := client.baseURL + "/proxy/api/session?token=" + authToken
	resp, err = client.http.Get(selfURL)
	if err != nil {
		t.Logf("Session error: %v", err)
	} else {
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Logf("\nSession status: %d", resp.StatusCode)
		if len(body) < 5000 {
			t.Logf("Session: %s", string(body))
		} else {
			t.Logf("Session (first 5000): %s", string(body[:5000]))
		}
	}
}
