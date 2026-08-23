package reporename

import (
	"strings"
	"testing"
)

func TestNormalizeNamePolicy(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		want  string
		err   bool
	}{
		{name: "trim and unicode", input: "  새 이름 •  ", want: "새 이름 •"},
		{name: "empty", input: " \t", err: true},
		{name: "leading dash", input: "-name", err: true},
		{name: "nul", input: "a\x00b", err: true},
		{name: "newline", input: "a\nb", err: true},
		{name: "control", input: "a\u000bb", err: true},
		{name: "too long", input: strings.Repeat("가", MaxNameRunes+1), err: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeName(test.input)
			if test.err {
				if err == nil {
					t.Fatalf("NormalizeName(%q) accepted", test.input)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("NormalizeName(%q) = %q, %v; want %q", test.input, got, err, test.want)
			}
		})
	}
}

func TestBuildCommandUsesExactPathAndNoShellOrFlagTerminator(t *testing.T) {
	command, err := BuildCommand("C:\\Program Files\\roborev.exe", Request{Path: `E:\tracked repo`, NewName: "이름 with spaces"})
	if err != nil {
		t.Fatal(err)
	}
	if command.Dir != "" {
		t.Fatalf("Dir=%q, want empty", command.Dir)
	}
	want := []string{"repo", "rename", `E:\tracked repo`, "이름 with spaces"}
	if len(command.Args) != len(want) {
		t.Fatalf("args=%#v", command.Args)
	}
	for i := range want {
		if command.Args[i] != want[i] {
			t.Fatalf("args[%d]=%q want %q", i, command.Args[i], want[i])
		}
	}
}
