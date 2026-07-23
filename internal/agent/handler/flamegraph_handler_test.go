package handler

import (
	"strings"
	"testing"
)

func TestParsePerfScriptProducesRootToLeafFoldedStacks(t *testing.T) {
	raw := []byte("mysqld 123/124 [001] 1.0: cycles:\n" +
		"        7f01 leaf+0x10 (/usr/bin/mysqld)\n" +
		"        7f02 middle+0x20 (/usr/bin/mysqld)\n" +
		"        7f03 root+0x30 (/usr/bin/mysqld)\n\n")
	got := parsePerfScript(raw)
	if got["process:mysqld;root;middle;leaf"] != 1 {
		t.Fatalf("unexpected folded stacks: %#v", got)
	}
}

func TestEncodeFoldedStacksIsStable(t *testing.T) {
	text, samples, stacks := encodeFoldedStacks(map[string]int64{"b;c": 2, "a;b": 3})
	if samples != 5 || stacks != 2 || text != "a;b 3\nb;c 2\n" {
		t.Fatalf("unexpected encoding samples=%d stacks=%d text=%q", samples, stacks, text)
	}
	if strings.Contains(text, "\r") {
		t.Fatal("folded output must use portable newlines")
	}
}
