package buildinfo

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

func TestCurrentDevelopmentMetadataIsDeterministic(t *testing.T) {
	oldVersion, oldCommit, oldRelease, oldTarget := version, commit, release, target
	defer func() { version, commit, release, target = oldVersion, oldCommit, oldRelease, oldTarget }()
	version, commit, release, target = "dev", "unknown", "false", ""

	got, err := Current()
	if err != nil {
		t.Fatalf("Current() error = %v", err)
	}
	if got.Name != Name || got.Version != "dev" || got.Commit != "unknown" || got.Release {
		t.Fatalf("Current() = %#v", got)
	}
	if want := runtime.GOOS + "/" + runtime.GOARCH; got.Target != want {
		t.Fatalf("Target = %q, want %q", got.Target, want)
	}
	if got.RoboRevCompatibility != SupportedRoboRev {
		t.Fatalf("RoboRevCompatibility = %q", got.RoboRevCompatibility)
	}
	first, err := got.JSON()
	if err != nil {
		t.Fatalf("JSON() error = %v", err)
	}
	second, err := got.JSON()
	if err != nil {
		t.Fatalf("second JSON() error = %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("JSON output changed: %q vs %q", first, second)
	}
	if want := `{"name":"roborev-dashboard","version":"dev","commit":"unknown","release":false,"target":"` + got.Target + `","roborevCompatibility":"v0.64.0"}`; string(first) != want {
		t.Fatalf("JSON = %s, want %s", first, want)
	}
}

func TestCurrentReleaseMetadataRequiresExactProvenance(t *testing.T) {
	oldVersion, oldCommit, oldRelease, oldTarget := version, commit, release, target
	defer func() { version, commit, release, target = oldVersion, oldCommit, oldRelease, oldTarget }()
	version, commit, release, target = "0.0.0-releasecheck.1", strings.Repeat("a", 40), "true", "windows/amd64"

	got, err := Current()
	if err != nil {
		t.Fatalf("Current() error = %v", err)
	}
	if !got.Release || got.Version != version || got.Commit != commit || got.Target != target {
		t.Fatalf("Current() = %#v", got)
	}
	encoded, err := got.JSON()
	if err != nil {
		t.Fatalf("JSON() error = %v", err)
	}
	var decoded Metadata
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("JSON is invalid: %v", err)
	}
	if decoded != got {
		t.Fatalf("decoded metadata = %#v, want %#v", decoded, got)
	}
}

func TestCurrentRejectsInvalidInjectedMetadata(t *testing.T) {
	oldVersion, oldCommit, oldRelease, oldTarget := version, commit, release, target
	defer func() { version, commit, release, target = oldVersion, oldCommit, oldRelease, oldTarget }()
	tests := []struct {
		name    string
		version string
		commit  string
		release string
		target  string
	}{
		{name: "release flag", version: "0.0.0", commit: strings.Repeat("a", 40), release: "yes", target: "windows/amd64"},
		{name: "release version", version: "v1.0.0", commit: strings.Repeat("a", 40), release: "true", target: "windows/amd64"},
		{name: "release commit", version: "1.0.0", commit: strings.Repeat("A", 40), release: "true", target: "windows/amd64"},
		{name: "release target", version: "1.0.0", commit: strings.Repeat("a", 40), release: "true", target: "linux/amd64"},
		{name: "development version", version: "1.0.0", commit: "unknown", release: "false", target: ""},
		{name: "development commit", version: "dev", commit: "not-a-sha", release: "false", target: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			version, commit, release, target = test.version, test.commit, test.release, test.target
			if _, err := Current(); err == nil {
				t.Fatal("Current() unexpectedly succeeded")
			}
		})
	}
}

func TestIsStrictSemVer(t *testing.T) {
	valid := []string{"0.0.0", "1.2.3", "10.20.30-alpha.1", "1.2.3+build.7", "1.2.3-rc.1+build.7"}
	for _, value := range valid {
		if !IsStrictSemVer(value) {
			t.Errorf("IsStrictSemVer(%q) = false", value)
		}
	}
	invalid := []string{"", "dev", "v1.2.3", "01.2.3", "1.2", "1.2.3-", "1.2.3-01", "1.2.3+", "1.2.3/unsafe", "1.2.3-rc_1"}
	for _, value := range invalid {
		if IsStrictSemVer(value) {
			t.Errorf("IsStrictSemVer(%q) = true", value)
		}
	}
}
