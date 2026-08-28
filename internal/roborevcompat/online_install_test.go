//go:build windows

package roborevcompat

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOnlinePinnedInstallerE2E(t *testing.T) {
	if os.Getenv("ROBOREV_DASHBOARD_ONLINE_INSTALL_E2E") != "1" {
		t.Skip("set ROBOREV_DASHBOARD_ONLINE_INSTALL_E2E=1 to run the official pinned download E2E")
	}
	manifest := Manifest064()
	if manifest.URL != "https://github.com/kenn-io/roborev/releases/download/v0.64.0/roborev_0.64.0_windows_amd64.zip" || manifest.CompressedSize != 16028741 || manifest.SHA256 != "7a8ea79ffd3a90634e89a654e72ee0f65ea941f23106005710145ddd551290f1" || manifest.ArchiveEntry != "roborev.exe" || manifest.TargetName != "roborev.exe" {
		t.Fatalf("Manifest064 changed from the pinned v0.64.0 Windows amd64 contract: %#v", manifest)
	}
	home := t.TempDir()
	installer := Installer{Downloader: Downloader{}, Home: home, Manifest: manifest, GOOS: "windows", GOARCH: "amd64"}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	target, err := installer.Install(ctx)
	cancel()
	if err != nil {
		// A failed official request is evidence missing, not a reason to report
		// a fake pass. Keep the test failing so CI/release operators see it.
		t.Fatalf("pinned RoboRev v0.64.0 install failed: %v", err)
	}
	wantTarget := filepath.Join(home, ".roborev", "bin", "roborev.exe")
	if target != wantTarget {
		t.Fatalf("installer target = %q, want %q", target, wantTarget)
	}
	if !isWithinDirectory(target, home) {
		t.Fatalf("installer escaped temporary HOME: %q", target)
	}
	info, err := os.Stat(target)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		t.Fatalf("installed target is not a non-empty regular file: %v", err)
	}
	beforeHash, err := sha256File(target)
	if err != nil {
		t.Fatal(err)
	}
	verifyInstalledRoboRev(t, target, home)

	secondCtx, secondCancel := context.WithTimeout(context.Background(), 30*time.Second)
	_, secondErr := installer.Install(secondCtx)
	secondCancel()
	if !errors.Is(secondErr, ErrExistingBinary) {
		t.Fatalf("second install error = %v, want ErrExistingBinary", secondErr)
	}
	afterHash, err := sha256File(target)
	if err != nil {
		t.Fatal(err)
	}
	if beforeHash != afterHash {
		t.Fatalf("second install changed managed binary: before=%s after=%s", beforeHash, afterHash)
	}
}

func verifyInstalledRoboRev(t *testing.T, target, home string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, target, "version", "--json")
	cmd.Env = isolatedInstallerEnvironment(home)
	var stdout, stderr boundedOutput
	stdout.limit, stderr.limit = 64<<10, 64<<10
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		t.Fatalf("installed RoboRev version --json failed: %v\n%s", err, stderr.Bytes())
	}
	if stdout.exceeded || stderr.exceeded {
		t.Fatalf("installed RoboRev version output exceeded bound: stdout=%d+ stderr=%d+", stdout.Len(), stderr.Len())
	}
	var identity struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	decoder := json.NewDecoder(strings.NewReader(stdout.String()))
	if err := decoder.Decode(&identity); err != nil {
		t.Fatalf("installed RoboRev version response is malformed: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("installed RoboRev version response has trailing data: %v", err)
	}
	if identity.Name != "roborev" || identity.Version != "v0.64.0" {
		t.Fatalf("installed RoboRev identity = %#v, want roborev v0.64.0", identity)
	}
}

// boundedOutput keeps an unexpected child response from becoming an
// unbounded test allocation. Excess bytes are discarded but recorded so the
// machine-readable version contract fails explicitly.
type boundedOutput struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *boundedOutput) Write(data []byte) (int, error) {
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		b.exceeded = true
		return len(data), nil
	}
	if len(data) > remaining {
		_, _ = b.Buffer.Write(data[:remaining])
		b.exceeded = true
		return len(data), nil
	}
	return b.Buffer.Write(data)
}

func isolatedInstallerEnvironment(home string) []string {
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	temp := filepath.Join(home, "tmp")
	appData := filepath.Join(home, "AppData", "Roaming")
	localAppData := filepath.Join(home, "AppData", "Local")
	for _, path := range []string{temp, appData, localAppData} {
		_ = os.MkdirAll(path, 0o700)
	}
	return []string{
		"SystemRoot=" + systemRoot,
		"SystemDrive=" + os.Getenv("SystemDrive"),
		"ComSpec=" + filepath.Join(systemRoot, "System32", "cmd.exe"),
		"PATHEXT=.COM;.EXE;.BAT;.CMD",
		"PATH=" + strings.Join([]string{filepath.Join(systemRoot, "System32"), systemRoot}, string(os.PathListSeparator)),
		"USERPROFILE=" + home,
		"HOME=" + home,
		"HOMEDRIVE=" + filepath.VolumeName(home),
		"HOMEPATH=" + strings.TrimPrefix(home, filepath.VolumeName(home)),
		"APPDATA=" + appData,
		"LOCALAPPDATA=" + localAppData,
		"TEMP=" + temp,
		"TMP=" + temp,
		"NO_COLOR=1",
	}
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func isWithinDirectory(path, root string) bool {
	path, root = filepath.Clean(path), filepath.Clean(root)
	if strings.EqualFold(path, root) {
		return true
	}
	return strings.HasPrefix(strings.ToLower(path), strings.ToLower(root)+string(filepath.Separator))
}
