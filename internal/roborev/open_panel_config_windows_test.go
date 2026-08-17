//go:build windows

package roborev

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

func TestWindowsExtendedPathContract(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		want       string
		wantPrefix string
	}{
		{name: "drive absolute", path: `C:\repo\config.toml`, want: `\\?\C:\repo\config.toml`},
		{name: "UNC", path: `\\server\share\repo\config.toml`, want: `\\?\UNC\server\share\repo\config.toml`},
		{name: "already extended drive", path: `\\?\C:\repo\config.toml`, want: `\\?\C:\repo\config.toml`},
		{name: "already extended UNC", path: `\\?\UNC\server\share\repo\config.toml`, want: `\\?\UNC\server\share\repo\config.toml`},
		{name: "relative", path: `repo\config.toml`, wantPrefix: `\\?\`},
		{name: "drive relative", path: `C:repo\config.toml`, wantPrefix: `\\?\`},
		{name: "rooted", path: `\repo\config.toml`, wantPrefix: `\\?\`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := windowsExtendedPath(test.path)
			if err != nil {
				t.Fatal(err)
			}
			if test.want != "" && got != test.want {
				t.Fatalf("windowsExtendedPath(%q) = %q, want %q", test.path, got, test.want)
			}
			if test.wantPrefix != "" && !strings.HasPrefix(got, test.wantPrefix) {
				t.Fatalf("windowsExtendedPath(%q) = %q, want prefix %q", test.path, got, test.wantPrefix)
			}
			if strings.Count(got, `\\?\`) != 1 {
				t.Fatalf("windowsExtendedPath(%q) = %q, has duplicate extended prefix", test.path, got)
			}
			if test.want == "" && !filepath.IsAbs(strings.TrimPrefix(got, `\\?\`)) {
				t.Fatalf("normalized path is not absolute: %q", got)
			}
		})
	}
}

func TestWindowsExtendedPathSkipsResolutionForAbsolutePaths(t *testing.T) {
	resolverCalls := 0
	failingResolver := func(string) (string, error) {
		resolverCalls++
		return "", errors.New("unexpected absolute path resolution")
	}

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "long drive absolute", path: `C:\` + strings.Repeat("long-panel-component-", 20) + `.toml`, want: `\\?\C:\` + strings.Repeat("long-panel-component-", 20) + `.toml`},
		{name: "absolute UNC", path: `\\server\share\` + strings.Repeat("long-panel-component-", 20) + `.toml`, want: `\\?\UNC\server\share\` + strings.Repeat("long-panel-component-", 20) + `.toml`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := windowsExtendedPathWithAbs(test.path, failingResolver)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("windowsExtendedPathWithAbs(%q) = %q, want %q", test.path, got, test.want)
			}
		})
	}
	if resolverCalls != 0 {
		t.Fatalf("absolute paths invoked resolver %d times, want 0", resolverCalls)
	}
}

func TestWindowsExtendedPathResolvesNonAbsolutePathsOnce(t *testing.T) {
	for _, path := range []string{`repo\config.toml`, `C:repo\config.toml`, `\repo\config.toml`} {
		t.Run(strings.ReplaceAll(path, "\\", "_"), func(t *testing.T) {
			resolverCalls := 0
			resolver := func(got string) (string, error) {
				resolverCalls++
				if got != path {
					t.Errorf("resolver received %q, want %q", got, path)
				}
				return `C:\resolved\config.toml`, nil
			}
			if _, err := windowsExtendedPathWithAbs(path, resolver); err != nil {
				t.Fatal(err)
			}
			if resolverCalls != 1 {
				t.Fatalf("non-absolute path invoked resolver %d times, want 1", resolverCalls)
			}
		})
	}
}

func TestWindowsExtendedPathRejectsBlankAndDeviceNamespace(t *testing.T) {
	for _, path := range []string{"", " \t\r\n", `\\.\COM1`, `\\.\PIPE\roborev`, "//./COM1"} {
		t.Run(strings.ReplaceAll(path, "\\", "_"), func(t *testing.T) {
			if got, err := windowsExtendedPath(path); err == nil {
				t.Fatalf("windowsExtendedPath(%q) = %q, want error", path, got)
			}
		})
	}
}

func TestWindowsLongPanelConfigOpensAndDecodes(t *testing.T) {
	base := t.TempDir()
	root := base
	for utf16PathLen(filepath.Join(root, ".roborev.toml")) <= 320 {
		root = filepath.Join(root, "long-panel-component-한글-0123456789abcdef")
	}
	configPath := filepath.Join(root, ".roborev.toml")
	if got := utf16PathLen(configPath); got <= 320 {
		t.Fatalf("test path length = %d UTF-16 code units, want > 320: %q", got, configPath)
	}

	extendedRoot, err := extendedSetupPath(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(extendedRoot, 0o700); err != nil {
		skipWindowsLongPathUnavailable(t, err)
	}
	extendedConfig, err := extendedSetupPath(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extendedConfig, []byte("[review.panels.long]\nmembers=[]\n"), 0o600); err != nil {
		skipWindowsLongPathUnavailable(t, err)
	}

	handle, err := openPanelConfigNoFollow(configPath)
	if err != nil {
		t.Fatalf("long panel config could not be opened: %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
	config, present, err := loadPanelConfig(configPath)
	if err != nil {
		t.Fatalf("long panel config could not be decoded: %v", err)
	}
	if !present {
		t.Fatal("long panel config was not found")
	}
	if _, ok := config.Panels["long"]; !ok {
		t.Fatalf("decoded panel config = %#v, want long panel", config)
	}
}

func extendedSetupPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("test setup path is not absolute")
	}
	clean := filepath.Clean(path)
	if strings.HasPrefix(clean, `\\`) {
		return `\\?\UNC\` + strings.TrimPrefix(clean, `\\`), nil
	}
	return `\\?\` + clean, nil
}

func utf16PathLen(path string) int {
	return len(utf16.Encode([]rune(path)))
}

func skipWindowsLongPathUnavailable(t *testing.T, err error) {
	t.Helper()
	if errors.Is(err, windows.ERROR_FILENAME_EXCED_RANGE) ||
		errors.Is(err, windows.ERROR_PATH_NOT_FOUND) ||
		errors.Is(err, windows.ERROR_INVALID_NAME) ||
		errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
		t.Skipf("Windows long paths are unavailable in this environment: %v", err)
	}
	t.Fatal(err)
}
