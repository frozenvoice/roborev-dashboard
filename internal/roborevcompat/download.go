package roborevcompat

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

const (
	maxRedirects       = 4
	maxArchiveSize     = 64 << 20
	maxExecutableSize  = 64 << 20
	allowedReleaseHost = "github.com"
	allowedAssetHost   = "release-assets.githubusercontent.com"
)

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Downloader struct {
	HTTP HTTPDoer
}

func (d Downloader) Download(ctx context.Context, manifest Manifest) ([]byte, error) {
	if err := validateManifestURL(manifest.URL, true); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRedirectPolicy, err)
	}
	if manifest.CompressedSize <= 0 || manifest.CompressedSize > maxArchiveSize || len(manifest.SHA256) != sha256.Size*2 {
		return nil, fmt.Errorf("%w: invalid manifest bounds", ErrDownloadContract)
	}
	client := d.HTTP
	if client == nil {
		client = productionHTTPClient()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifest.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDownloadContract, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("download RoboRev archive canceled: %w", ctxErr)
		}
		// Do not return the transport error: redirect implementations may put
		// signed query material in its URL-bearing message.
		return nil, fmt.Errorf("%w: download transport failed", ErrDownloadContract)
	}
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("%w: empty response", ErrDownloadContract)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: release returned HTTP %d", ErrDownloadContract, resp.StatusCode)
	}
	if resp.ContentLength >= 0 && resp.ContentLength != manifest.CompressedSize {
		return nil, fmt.Errorf("%w: compressed size does not match manifest", ErrDownloadSize)
	}
	limited := io.LimitReader(resp.Body, manifest.CompressedSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("%w: read archive: %v", ErrDownloadContract, err)
	}
	if int64(len(data)) != manifest.CompressedSize {
		return nil, fmt.Errorf("%w: received archive length is outside the pinned size", ErrDownloadSize)
	}
	hash := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(hash[:]), manifest.SHA256) {
		return nil, fmt.Errorf("%w: SHA-256 mismatch", ErrDownloadHash)
	}
	return data, nil
}

func redirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("%w: too many redirects", ErrRedirectPolicy)
	}
	if err := validateManifestURL(req.URL.String(), false); err != nil {
		return fmt.Errorf("%w: redirect rejected", ErrRedirectPolicy)
	}
	return nil
}

func productionHTTPClient() *http.Client {
	transport := &http.Transport{
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	}
	return &http.Client{Timeout: 2 * time.Minute, CheckRedirect: redirectPolicy, Transport: transport}
}

func validateManifestURL(raw string, initial bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Path == "" {
		return errors.New("URL must be HTTPS without userinfo or fragment")
	}
	if port := u.Port(); port != "" && port != "443" {
		return errors.New("URL must use port 443")
	}
	host := strings.ToLower(u.Hostname())
	if initial {
		if host != allowedReleaseHost {
			return errors.New("initial URL host is not the pinned GitHub release host")
		}
	} else if host != allowedReleaseHost && host != allowedAssetHost {
		return errors.New("redirect host is not allowlisted")
	}
	return nil
}

func ExtractExecutable(archive []byte, manifest Manifest) ([]byte, error) {
	if int64(len(archive)) != manifest.CompressedSize {
		return nil, fmt.Errorf("%w: compressed length mismatch", ErrArchiveContract)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid ZIP: %v", ErrArchiveContract, err)
	}
	if len(reader.File) != 1 {
		return nil, fmt.Errorf("%w: archive must contain exactly one entry", ErrArchiveContract)
	}
	entry := reader.File[0]
	if entry.Name != manifest.ArchiveEntry || path.Base(entry.Name) != entry.Name || strings.ContainsAny(entry.Name, `/\\`) || entry.Flags&0x1 != 0 || entry.FileInfo().Mode()&os.ModeSymlink != 0 || !entry.FileInfo().Mode().IsRegular() {
		return nil, fmt.Errorf("%w: archive entry is not the expected regular root executable", ErrArchiveContract)
	}
	if entry.UncompressedSize64 == 0 || entry.UncompressedSize64 > maxExecutableSize || entry.CompressedSize64 == 0 || entry.UncompressedSize64 > entry.CompressedSize64*100 {
		return nil, fmt.Errorf("%w: executable size is out of bounds", ErrArchiveContract)
	}
	file, err := entry.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: open executable: %v", ErrArchiveContract, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxExecutableSize+1))
	if err != nil {
		if errors.Is(err, zip.ErrChecksum) {
			return nil, fmt.Errorf("%w: %w", ErrArchiveContract, ErrArchiveCRC)
		}
		return nil, fmt.Errorf("%w: read executable: %v", ErrArchiveContract, err)
	}
	if int64(len(data)) != int64(entry.UncompressedSize64) || len(data) > maxExecutableSize {
		return nil, fmt.Errorf("%w: executable size changed while reading", ErrArchiveContract)
	}
	return data, nil
}
