package repoinventory

import (
	"strconv"
	"strings"
	"testing"
)

func TestParseValidAndEmptyInventory(t *testing.T) {
	valid := []byte(`{"repos":[{"name":"one","root_path":"E:/one","identity":"id-1","count":2},{"name":"one","root_path":"E:\\two","count":0}],"total_count":2}`)
	parsed, err := Parse(valid)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Repositories) != 2 || parsed.TotalCount != 2 {
		t.Fatalf("parsed=%#v", parsed)
	}
	if parsed.Repositories[0].Identity != "id-1" || parsed.Repositories[1].RootPath != `E:\two` || parsed.Repositories[1].Count != 0 {
		t.Fatalf("repositories=%#v", parsed.Repositories)
	}
	empty, err := Parse([]byte(`{"repos":[],"total_count":0}`))
	if err != nil || empty.Repositories == nil || len(empty.Repositories) != 0 || empty.TotalCount != 0 {
		t.Fatalf("empty=%#v err=%v", empty, err)
	}
}

func TestParseRejectsMalformedAndUnsafeShapes(t *testing.T) {
	validRow := `{"name":"repo","root_path":"E:/repo","count":1}`
	tests := []struct {
		name string
		body string
	}{
		{"empty body", ``},
		{"null", `null`},
		{"array", `[]`},
		{"top-level scalar", `1`},
		{"empty object", `{}`},
		{"missing repos", `{"total_count":0}`},
		{"missing total", `{"repos":[]}`},
		{"repos null", `{"repos":null,"total_count":0}`},
		{"repos scalar", `{"repos":"not-an-array","total_count":0}`},
		{"row null", `{"repos":[null],"total_count":0}`},
		{"row array", `{"repos":[[]],"total_count":0}`},
		{"missing name", `{"repos":[{"root_path":"E:/repo","count":0}],"total_count":0}`},
		{"name null", `{"repos":[{"name":null,"root_path":"E:/repo","count":0}],"total_count":0}`},
		{"wrong name type", `{"repos":[{"name":1,"root_path":"E:/repo","count":0}],"total_count":0}`},
		{"missing root path", `{"repos":[{"name":"repo","count":0}],"total_count":0}`},
		{"empty root path", `{"repos":[{"name":"repo","root_path":"","count":0}],"total_count":0}`},
		{"root path null", `{"repos":[{"name":"repo","root_path":null,"count":0}],"total_count":0}`},
		{"root path number", `{"repos":[{"name":"repo","root_path":7,"count":0}],"total_count":0}`},
		{"count null", `{"repos":[{"name":"repo","root_path":"E:/repo","count":null}],"total_count":0}`},
		{"wrong count type", `{"repos":[{"name":"repo","root_path":"E:/repo","count":"1"}],"total_count":0}`},
		{"negative count", `{"repos":[{"name":"repo","root_path":"E:/repo","count":-1}],"total_count":0}`},
		{"fractional count", `{"repos":[{"name":"repo","root_path":"E:/repo","count":1.5}],"total_count":1}`},
		{"exponent count", `{"repos":[{"name":"repo","root_path":"E:/repo","count":1e1}],"total_count":10}`},
		{"unsafe count", `{"repos":[{"name":"repo","root_path":"E:/repo","count":9007199254740992}],"total_count":9007199254740992}`},
		{"total null", `{"repos":[],"total_count":null}`},
		{"total string", `{"repos":[],"total_count":"0"}`},
		{"negative total", `{"repos":[],"total_count":-1}`},
		{"fractional total", `{"repos":[],"total_count":1.5}`},
		{"unsafe total", `{"repos":[],"total_count":9007199254740992}`},
		{"sum mismatch", `{"repos":[` + validRow + `],"total_count":0}`},
		{"unknown top field", `{"repos":[],"total_count":0,"extra":true}`},
		{"unknown row field", `{"repos":[{"name":"repo","root_path":"E:/repo","count":0,"extra":true}],"total_count":0}`},
		{"identity null", `{"repos":[{"name":"repo","root_path":"E:/repo","identity":null,"count":0}],"total_count":0}`},
		{"identity number", `{"repos":[{"name":"repo","root_path":"E:/repo","identity":7,"count":0}],"total_count":0}`},
		{"trailing json", `{"repos":[],"total_count":0}{}`},
		{"duplicate field", `{"repos":[],"total_count":0,"total_count":0}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Parse([]byte(test.body)); err == nil {
				t.Fatalf("accepted malformed body %s", test.body)
			}
		})
	}
}

func TestParseRejectsInvalidUTF8AndOversize(t *testing.T) {
	invalid := []byte(`{"repos":[{"name":"`)
	invalid = append(invalid, 0xff)
	invalid = append(invalid, []byte(`","root_path":"E:/repo","count":0}],"total_count":0}`)...)
	if _, err := Parse(invalid); err == nil {
		t.Fatal("accepted invalid UTF-8")
	}
	oversize := []byte(`{"repos":[],"total_count":0,"padding":"` + strings.Repeat("a", MaxResponseBytes) + `"}`)
	if len(oversize) <= MaxResponseBytes {
		t.Fatalf("test body did not exceed bound: %d", len(oversize))
	}
	if _, err := Parse(oversize); err == nil {
		t.Fatal("accepted oversized body")
	}
	safe := `{"repos":[{"name":"repo","root_path":"E:/repo","count":` + strconv.FormatInt(MaxSafeInteger, 10) + `}],"total_count":` + strconv.FormatInt(MaxSafeInteger, 10) + `}`
	if _, err := Parse([]byte(safe)); err != nil {
		t.Fatalf("safe maximum was rejected: %v", err)
	}
}
