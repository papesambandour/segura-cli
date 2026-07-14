package cmd

import (
	"reflect"
	"testing"
)

func TestSplitArgs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"ls", []string{"ls"}},
		{"ls /var/log", []string{"ls", "/var/log"}},
		{"  cd    ..  ", []string{"cd", ".."}},
		{`get "/var/My Files/a.txt" ./a.txt`, []string{"get", "/var/My Files/a.txt", "./a.txt"}},
		{`put './local file.txt' '/tmp/remote file.txt'`, []string{"put", "./local file.txt", "/tmp/remote file.txt"}},
		{`echo ab"cd"ef`, []string{"echo", "abcdef"}}, // adjacent quoted+unquoted join
	}
	for _, c := range cases {
		got := splitArgs(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitArgs(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{
		0:          "0B",
		512:        "512B",
		1024:       "1.0KB",
		1536:       "1.5KB",
		1048576:    "1.0MB",
		1073741824: "1.0GB",
	}
	for in, want := range cases {
		if got := humanSize(in); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", in, got, want)
		}
	}
}
