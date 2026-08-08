package webserver

import (
	"fmt"
	"testing"
)

// guacElem formats one Guacamole element (LENGTH.VALUE).
func guacElem(s string) string { return fmt.Sprintf("%d.%s", len(s), s) }

// msgboxFrame builds a well-formed "msgbox" instruction frame.
func msgboxFrame(sev, msg string) string {
	return guacElem("msgbox") + "," + guacElem(sev) + "," + guacElem(msg) + ";"
}

func TestParseGuacMsgbox(t *testing.T) {
	realMsg := "Decryption proccess error: error:03000082:digital envelope routines::invalid key"

	cases := []struct {
		frame    string
		sev, msg string
		found    bool
	}{
		{msgboxFrame("Error", "Decryption failed ok"), "Error", "Decryption failed ok", true},
		{msgboxFrame("Error", realMsg), "Error", realMsg, true},
		// preceded by another instruction in the same frame
		{"4.sync,4.1234;" + msgboxFrame("Error", "oops!"), "Error", "oops!", true},
		// normal terminal traffic: no msgbox
		{"4.sync,4.1234;3.img,1.0,4.blob;", "", "", false},
		{"3.nop;", "", "", false},
		// malformed length must not panic and must not falsely match
		{"99.short;", "", "", false},
		{"6.msgbox,5.Error,87.tooShortForClaimedLength;", "", "", false},
	}
	for _, c := range cases {
		sev, msg, found := parseGuacMsgbox([]byte(c.frame))
		if found != c.found || (c.found && (sev != c.sev || msg != c.msg)) {
			t.Errorf("parseGuacMsgbox(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.frame, sev, msg, found, c.sev, c.msg, c.found)
		}
	}
}
