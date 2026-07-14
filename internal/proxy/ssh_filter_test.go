package proxy

import "testing"

// feed runs the whole input through the filter in one call.
func filterAll(in string) string {
	var f daSequenceFilter
	return string(f.strip([]byte(in)))
}

func TestDAResponseFilter_StripsDAReplies(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// The exact loop culprit.
		{"single DA reply", "\x1b[?6c", ""},
		{"DA reply with params", "\x1b[?1;2c", ""},
		{"flood", "\x1b[?6c\x1b[?6c\x1b[?6c\x1b[?6c", ""},
		{"DA reply surrounded by text", "ab\x1b[?6ccd", "abcd"},

		// Must pass through untouched.
		{"plain text", "hello world", "hello world"},
		{"typed zero", "0000", "0000"},
		{"newline+cr", "ls\r\n", "ls\r\n"},
		{"up arrow", "\x1b[A", "\x1b[A"},
		{"down arrow", "\x1b[B", "\x1b[B"},
		{"function key", "\x1b[1;2A", "\x1b[1;2A"},
		{"cursor position report", "\x1b[24;80R", "\x1b[24;80R"},
		{"DECRQM report (not DA)", "\x1b[?2004;1$y", "\x1b[?2004;1$y"},
		{"bare ESC", "\x1b", ""}, // held, never resolves -> dropped at EOF (acceptable)
		{"esc then text", "\x1bOP", "\x1bOP"},
		{"double esc then arrow", "\x1b\x1b[A", "\x1b\x1b[A"},

		// CRITICAL: private-mode toggles end in h/l, NOT c — must survive, or the
		// terminal display breaks (cursor, bracketed paste, vim's alt screen).
		{"cursor hide", "\x1b[?25l", "\x1b[?25l"},
		{"cursor show", "\x1b[?25h", "\x1b[?25h"},
		{"bracketed paste on", "\x1b[?2004h", "\x1b[?2004h"},
		{"alt screen (vim)", "\x1b[?1049h", "\x1b[?1049h"},
		{"mixed frame", "\x1b[?25l\x1b[?6cX\x1b[?25h", "\x1b[?25lX\x1b[?25h"},
	}
	for _, c := range cases {
		if got := filterAll(c.in); got != c.want {
			t.Errorf("%s: strip(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// The sequence is split across two reads mid-escape; the state machine must
// still strip it and not corrupt the surrounding bytes.
func TestDAResponseFilter_SplitAcrossReads(t *testing.T) {
	var f daSequenceFilter
	var out []byte
	out = append(out, f.strip([]byte("x\x1b[?"))...) // partial sequence at end
	out = append(out, f.strip([]byte("6cy"))...)     // completes the DA reply
	if string(out) != "xy" {
		t.Fatalf("split DA reply not stripped: got %q, want %q", string(out), "xy")
	}
}

// A partial arrow key split across reads must still pass through intact.
func TestDAResponseFilter_SplitArrowKey(t *testing.T) {
	var f daSequenceFilter
	var out []byte
	out = append(out, f.strip([]byte("\x1b["))...)
	out = append(out, f.strip([]byte("A"))...)
	if string(out) != "\x1b[A" {
		t.Fatalf("split arrow key corrupted: got %q, want %q", string(out), "\x1b[A")
	}
}
