package dynamic

import (
	"testing"
)

func TestParseCommandOutput(t *testing.T) {
	cases := []struct {
		name   string
		parser string
		stdout string
		code   int
		params map[string]string
		want   any
	}{
		{name: "raw", parser: "raw_string", stdout: "active", want: "active"},
		{name: "bool", parser: "bool_by_exit_code", code: 0, want: true},
		{name: "int", parser: "int", stdout: "42", want: 42},
		{name: "regex", parser: "regex_extract", stdout: "usage=77%", params: map[string]string{"regex": `usage=([0-9]+)%`}, want: "77"},
	}
	for _, tc := range cases {
		got, _, err := ParseCommandOutput(tc.parser, tc.stdout, "", tc.code, tc.params)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s: got %#v want %#v", tc.name, got, tc.want)
		}
	}
}
