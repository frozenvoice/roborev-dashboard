// Package repoinventory contains the bounded, strict decoder for RoboRev's
// v0.64 GET /api/repos response.  It deliberately has no dependency on the
// Dashboard registry or HTTP server so the upstream contract can be tested in
// isolation.
package repoinventory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// MaxResponseBytes is the maximum body accepted from the daemon.  The
	// endpoint is an inventory summary, so a one MiB bound is intentionally
	// generous while preventing an unbounded response from entering memory.
	MaxResponseBytes = 1 << 20
	// MaxSafeInteger is the largest integer that can be represented exactly by
	// a JavaScript Number, which is the browser DTO's numeric safety boundary.
	MaxSafeInteger int64 = 9007199254740991
)

var ErrMalformed = errors.New("malformed RoboRev repository inventory response")

var ErrAmbiguousRoot = errors.New("repository inventory contains ambiguous roots")

// Repository is one exact v0.64 RepoWithCount row. Identity is retained only
// for typed upstream decoding; Dashboard authorization must never use it.
type Repository struct {
	Name     string `json:"name"`
	RootPath string `json:"root_path"`
	Identity string `json:"identity,omitempty"`
	Count    int64  `json:"count"`
}

// Inventory is the exact v0.64 /api/repos envelope. TotalCount is the total
// number of review_jobs rows, not the number of repositories.
type Inventory struct {
	Repositories []Repository `json:"repos"`
	TotalCount   int64        `json:"total_count"`
}

// Parse decodes one bounded, exact-shape v0.64 inventory response.
func Parse(raw []byte) (Inventory, error) {
	if len(raw) == 0 || len(raw) > MaxResponseBytes {
		return Inventory{}, fmt.Errorf("%w: body exceeds %d bytes", ErrMalformed, MaxResponseBytes)
	}
	if !utf8.Valid(raw) {
		return Inventory{}, fmt.Errorf("%w: body is not valid UTF-8", ErrMalformed)
	}
	value, err := parseOne(raw)
	if err != nil {
		return Inventory{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	root, ok := value.(map[string]any)
	if !ok {
		return Inventory{}, fmt.Errorf("%w: top-level value must be an object", ErrMalformed)
	}
	if err := validateObjectFields(root, map[string]bool{"repos": true, "total_count": true}); err != nil {
		return Inventory{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	reposValue, ok := root["repos"]
	if !ok {
		return Inventory{}, fmt.Errorf("%w: repos is required", ErrMalformed)
	}
	reposRaw, ok := reposValue.([]any)
	if !ok {
		return Inventory{}, fmt.Errorf("%w: repos must be an array", ErrMalformed)
	}
	totalValue, ok := root["total_count"]
	if !ok {
		return Inventory{}, fmt.Errorf("%w: total_count is required", ErrMalformed)
	}
	total, err := parseSafeInteger(totalValue, "total_count")
	if err != nil {
		return Inventory{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}

	result := Inventory{Repositories: make([]Repository, 0, len(reposRaw)), TotalCount: total}
	var sum int64
	for index, value := range reposRaw {
		row, ok := value.(map[string]any)
		if !ok {
			return Inventory{}, fmt.Errorf("%w: repos[%d] must be an object", ErrMalformed, index)
		}
		if err := validateObjectFields(row, map[string]bool{"name": true, "root_path": true, "identity": true, "count": true}); err != nil {
			return Inventory{}, fmt.Errorf("%w: repos[%d]: %v", ErrMalformed, index, err)
		}
		name, err := requiredString(row, "name")
		if err != nil {
			return Inventory{}, fmt.Errorf("%w: repos[%d]: %v", ErrMalformed, index, err)
		}
		rootPath, err := requiredString(row, "root_path")
		if err != nil {
			return Inventory{}, fmt.Errorf("%w: repos[%d]: %v", ErrMalformed, index, err)
		}
		if rootPath == "" {
			return Inventory{}, fmt.Errorf("%w: repos[%d]: root_path must not be empty", ErrMalformed, index)
		}
		countValue, ok := row["count"]
		if !ok {
			return Inventory{}, fmt.Errorf("%w: repos[%d]: count is required", ErrMalformed, index)
		}
		count, err := parseSafeInteger(countValue, fmt.Sprintf("repos[%d].count", index))
		if err != nil {
			return Inventory{}, fmt.Errorf("%w: %v", ErrMalformed, err)
		}
		identity := ""
		if identityValue, present := row["identity"]; present {
			identity, ok = identityValue.(string)
			if !ok {
				return Inventory{}, fmt.Errorf("%w: repos[%d].identity must be a string", ErrMalformed, index)
			}
		}
		if sum > MaxSafeInteger-count {
			return Inventory{}, fmt.Errorf("%w: repository counts exceed safe integer", ErrMalformed)
		}
		sum += count
		result.Repositories = append(result.Repositories, Repository{Name: name, RootPath: rootPath, Identity: identity, Count: count})
	}
	if sum != total {
		return Inventory{}, fmt.Errorf("%w: total_count %d does not equal repository count sum %d", ErrMalformed, total, sum)
	}
	return result, nil
}

// ParseJSON is an explicit adapter-facing alias.
func ParseJSON(raw []byte) (Inventory, error) { return Parse(raw) }

// MatchRoot validates the complete inventory and returns the number of rows
// whose canonical path is equivalent to root. Inventory rows are never merged;
// duplicate equivalent roots are an ambiguity and therefore an error. This
// keeps registration/readback authority independent from upstream display
// names, identities, and review counts.
func (i Inventory) MatchRoot(root string) (int, error) {
	if !validInventoryRoot(root) {
		return 0, ErrMalformed
	}
	if i.TotalCount < 0 || i.TotalCount > MaxSafeInteger {
		return 0, ErrMalformed
	}
	selected := inventoryRootKey(root)
	seen := make(map[string]struct{}, len(i.Repositories))
	found := 0
	var sum int64
	for _, repository := range i.Repositories {
		if !validInventoryRoot(repository.RootPath) {
			return 0, ErrMalformed
		}
		if repository.Count < 0 || repository.Count > MaxSafeInteger || sum > MaxSafeInteger-repository.Count {
			return 0, ErrMalformed
		}
		sum += repository.Count
		key := inventoryRootKey(repository.RootPath)
		if _, exists := seen[key]; exists {
			return 0, ErrAmbiguousRoot
		}
		seen[key] = struct{}{}
		if key == selected {
			found++
		}
	}
	if sum != i.TotalCount {
		return 0, ErrMalformed
	}
	return found, nil
}

func validInventoryRoot(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	if filepath.IsAbs(value) {
		return true
	}
	normalized := strings.ReplaceAll(value, "\\", "/")
	if strings.HasPrefix(normalized, "/") || strings.HasPrefix(normalized, "//") {
		return true
	}
	return len(normalized) >= 3 && ((normalized[0] >= 'a' && normalized[0] <= 'z') || (normalized[0] >= 'A' && normalized[0] <= 'Z')) && normalized[1] == ':' && normalized[2] == '/'
}

func inventoryRootKey(value string) string {
	normalized := strings.ReplaceAll(value, "\\", "/")
	clean := filepath.Clean(filepath.FromSlash(normalized))
	clean = strings.ReplaceAll(clean, "\\", "/")
	windowsStyle := runtime.GOOS == "windows" || strings.HasPrefix(normalized, "//") || (len(normalized) >= 2 && normalized[1] == ':')
	if windowsStyle {
		return strings.ToLower(clean)
	}
	return clean
}

func validateObjectFields(object map[string]any, allowed map[string]bool) error {
	for key := range object {
		if !allowed[key] {
			return fmt.Errorf("unknown field %q", key)
		}
	}
	return nil
}

func requiredString(object map[string]any, key string) (string, error) {
	value, ok := object[key]
	if !ok {
		return "", fmt.Errorf("%s is required", key)
	}
	stringValue, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return stringValue, nil
}

func parseSafeInteger(value any, field string) (int64, error) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, fmt.Errorf("%s must be a non-negative integer", field)
	}
	// Upstream emits JSON integers. Reject decimal and exponent spellings even
	// when they happen to denote an integer, keeping the contract unambiguous.
	digits := string(number)
	if digits == "" || (len(digits) > 1 && digits[0] == '0') || strings.IndexAny(digits, ".eE+-") >= 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", field)
	}
	parsed, err := strconv.ParseUint(digits, 10, 63)
	if err != nil || parsed > uint64(MaxSafeInteger) {
		return 0, fmt.Errorf("%s is outside the safe integer range", field)
	}
	return int64(parsed), nil
}

// parseOne rejects malformed, duplicate-key, and trailing JSON values. The
// standard struct decoder alone would silently accept duplicate keys.
func parseOne(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := parseValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, errors.New("trailing JSON value")
		}
		return nil, fmt.Errorf("trailing JSON: %w", err)
	}
	return value, nil
}

func parseValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			object := make(map[string]any)
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, errors.New("object key is not a string")
				}
				if _, exists := object[key]; exists {
					return nil, fmt.Errorf("duplicate field %q", key)
				}
				fieldValue, err := parseValue(decoder)
				if err != nil {
					return nil, err
				}
				object[key] = fieldValue
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return nil, errors.New("malformed object")
			}
			return object, nil
		case '[':
			array := make([]any, 0)
			for decoder.More() {
				item, err := parseValue(decoder)
				if err != nil {
					return nil, err
				}
				array = append(array, item)
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return nil, errors.New("malformed array")
			}
			return array, nil
		default:
			return nil, errors.New("unexpected JSON delimiter")
		}
	case nil, bool, string, json.Number:
		return value, nil
	default:
		return nil, errors.New("unsupported JSON value")
	}
}
