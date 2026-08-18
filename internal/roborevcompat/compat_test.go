package roborevcompat

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeHTTP func(*http.Request) (*http.Response, error)

func (f fakeHTTP) Do(r *http.Request) (*http.Response, error) { return f(r) }

func makeArchive(t *testing.T, mode os.FileMode, name string) []byte {
	return makeArchiveFlags(t, mode, name, 0, zip.Store, []byte("fake executable"))
}

func makeArchiveFlags(t *testing.T, mode os.FileMode, name string, flags uint16, method uint16, payload []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	header := &zip.FileHeader{Name: name, Method: method, Flags: flags}
	header.SetMode(mode)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func makeEmptyArchive(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := zip.NewWriter(&buffer).Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func makeMultipleArchive(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range []string{"roborev.exe", "other.txt"} {
		entry, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func testManifest(data []byte) Manifest {
	hash := sha256.Sum256(data)
	manifest := Manifest064()
	manifest.CompressedSize = int64(len(data))
	manifest.SHA256 = hex.EncodeToString(hash[:])
	return manifest
}

func TestDownloadChecksExactSizeAndHashWithoutExternalNetwork(t *testing.T) {
	archive := makeArchive(t, 0o700, "roborev.exe")
	manifest := testManifest(archive)
	data, err := (Downloader{HTTP: fakeHTTP(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, ContentLength: int64(len(archive)), Body: io.NopCloser(bytes.NewReader(archive))}, nil
	})}).Download(context.Background(), manifest)
	if err != nil || !bytes.Equal(data, archive) {
		t.Fatalf("download = %d bytes, %v", len(data), err)
	}
	manifest.CompressedSize++
	if _, err := (Downloader{HTTP: fakeHTTP(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, ContentLength: int64(len(archive)), Body: io.NopCloser(bytes.NewReader(archive))}, nil
	})}).Download(context.Background(), manifest); !errors.Is(err, ErrDownloadSize) {
		t.Fatalf("size mismatch error = %v", err)
	}
}

func TestDownloadAcceptsChunkedButRejectsOverflow(t *testing.T) {
	archive := makeArchive(t, 0o700, "roborev.exe")
	manifest := testManifest(archive)
	chunked := func(data []byte) fakeHTTP {
		return fakeHTTP(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, ContentLength: -1, Body: io.NopCloser(bytes.NewReader(data))}, nil
		})
	}
	if data, err := (Downloader{HTTP: chunked(archive)}).Download(context.Background(), manifest); err != nil || !bytes.Equal(data, archive) {
		t.Fatalf("chunked download = %d bytes, %v", len(data), err)
	}
	if _, err := (Downloader{HTTP: chunked(append(append([]byte{}, archive...), 1))}).Download(context.Background(), manifest); !errors.Is(err, ErrDownloadSize) {
		t.Fatalf("overflow error = %v", err)
	}
	if _, err := (Downloader{HTTP: chunked(archive[:len(archive)-1])}).Download(context.Background(), manifest); !errors.Is(err, ErrDownloadSize) {
		t.Fatalf("truncated chunked error = %v", err)
	}
	wrongHash := testManifest(archive)
	wrongHash.SHA256 = strings.Repeat("0", sha256.Size*2)
	if _, err := (Downloader{HTTP: chunked(archive)}).Download(context.Background(), wrongHash); !errors.Is(err, ErrDownloadHash) {
		t.Fatalf("hash mismatch error = %v", err)
	}
}

func TestDownloadRejectsUnsafeURLs(t *testing.T) {
	for _, raw := range []string{
		"http://github.com/kenn-io/roborev/releases/a.zip",
		"https://github.com:444/kenn-io/roborev/releases/a.zip",
		"https://evil.example/a.zip",
		"https://user:pass@github.com/a.zip",
		"https://github.com/a.zip#fragment",
	} {
		if err := validateManifestURL(raw, false); err == nil {
			t.Fatalf("unsafe URL accepted: %s", raw)
		}
	}
	if err := validateManifestURL("https://release-assets.githubusercontent.com/a.zip", false); err != nil {
		t.Fatal(err)
	}
}

func TestExtractExecutableRejectsArchiveShapeAndPreservesNoPartialTarget(t *testing.T) {
	valid := makeArchive(t, 0o700, "roborev.exe")
	manifest := testManifest(valid)
	data, err := ExtractExecutable(valid, manifest)
	if err != nil || string(data) != "fake executable" {
		t.Fatalf("extract = %q, %v", data, err)
	}
	for _, archive := range [][]byte{
		makeArchive(t, 0o700, "nested/roborev.exe"),
		makeArchive(t, os.ModeSymlink|0o777, "roborev.exe"),
		makeArchive(t, 0o644, "other.exe"),
	} {
		bad := testManifest(archive)
		if _, err := ExtractExecutable(archive, bad); !errors.Is(err, ErrArchiveContract) {
			t.Fatalf("unsafe archive accepted: %v", err)
		}
	}
	if _, err := ExtractExecutable(valid[:len(valid)-1], manifest); !errors.Is(err, ErrArchiveContract) {
		t.Fatalf("truncated archive error = %v", err)
	}
}

func TestExtractExecutableRejectsUnsafeAndCorruptZipContracts(t *testing.T) {
	valid := makeArchive(t, 0o700, "roborev.exe")
	cases := []struct {
		name    string
		archive []byte
	}{
		{"empty", makeEmptyArchive(t)},
		{"multiple", makeMultipleArchive(t)},
		{"traversal", makeArchive(t, 0o700, "../roborev.exe")},
		{"absolute", makeArchive(t, 0o700, "/roborev.exe")},
		{"backslash", makeArchive(t, 0o700, `..\\roborev.exe`)},
		{"wrong-name", makeArchive(t, 0o700, "roborev.dll")},
		{"encrypted", makeArchiveFlags(t, 0o700, "roborev.exe", 0x1, zip.Store, []byte("fake executable"))},
		{"ratio", makeArchiveFlags(t, 0o700, "roborev.exe", 0, zip.Deflate, bytes.Repeat([]byte("A"), 10000))},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			manifest := testManifest(test.archive)
			if _, err := ExtractExecutable(test.archive, manifest); !errors.Is(err, ErrArchiveContract) {
				t.Fatalf("archive accepted: %v", err)
			}
		})
	}
	corrupt := append([]byte(nil), valid...)
	if index := bytes.Index(corrupt, []byte("fake executable")); index >= 0 {
		corrupt[index] ^= 0x01
	} else {
		t.Fatal("test archive payload not found")
	}
	if _, err := ExtractExecutable(corrupt, testManifest(corrupt)); !errors.Is(err, ErrArchiveCRC) {
		t.Fatalf("CRC corruption error = %v", err)
	}
	if _, err := ExtractExecutable(valid[:len(valid)-2], testManifest(valid)); !errors.Is(err, ErrArchiveContract) {
		t.Fatalf("invalid zip error = %v", err)
	}
}

func TestInstallerRejectsUnsupportedArchitectureBeforeDownload(t *testing.T) {
	archive := makeArchive(t, 0o700, "roborev.exe")
	calls := 0
	for _, platform := range []struct{ goos, goarch string }{{"linux", "amd64"}, {"windows", "arm64"}} {
		t.Run(platform.goos+"-"+platform.goarch, func(t *testing.T) {
			calls = 0
			installer := Installer{Home: t.TempDir(), GOOS: platform.goos, GOARCH: platform.goarch, Manifest: testManifest(archive), Downloader: Downloader{HTTP: fakeHTTP(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: http.StatusOK, ContentLength: int64(len(archive)), Body: io.NopCloser(bytes.NewReader(archive))}, nil
			})}}
			if _, err := installer.Install(context.Background()); !errors.Is(err, ErrUnsupportedPlatform) {
				t.Fatalf("platform error = %v", err)
			}
			if calls != 0 {
				t.Fatalf("download calls = %d", calls)
			}
		})
	}
}

func TestInstallerUsesSafeStagingAndNeverOverwritesExistingBinary(t *testing.T) {
	archive := makeArchive(t, 0o700, "roborev.exe")
	manifest := testManifest(archive)
	home := t.TempDir()
	installer := Installer{Home: home, GOOS: "windows", GOARCH: "amd64", Manifest: manifest, Downloader: Downloader{HTTP: fakeHTTP(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, ContentLength: int64(len(archive)), Body: io.NopCloser(bytes.NewReader(archive))}, nil
	})}}
	target, err := installer.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "fake executable" {
		t.Fatalf("installed target = %q, %v", data, err)
	}
	if !strings.Contains(filepath.Base(target), "roborev.exe") {
		t.Fatalf("unexpected target %s", target)
	}
	if _, err := installer.Install(context.Background()); !errors.Is(err, ErrExistingBinary) {
		t.Fatalf("overwrite error = %v", err)
	}
}
