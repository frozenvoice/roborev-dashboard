// Package configuration owns the Dashboard's safe, allowlisted RoboRev
// configuration surface. The browser never decides keys, types, scopes, or
// validation; this package is the compile-time source of truth and rejects
// everything outside the v1 editable list.
package configuration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/froze/roborev-dashboard/internal/roborev"
)

// SettingType enumerates the value shapes the Dashboard knows how to validate.
type SettingType string

const (
	TypeInteger SettingType = "integer"
	TypeBoolean SettingType = "boolean"
	TypeEnum    SettingType = "enum"
)

// Scope selects which RoboRev configuration file a change targets.
type Scope string

const (
	ScopeGlobal Scope = "global"
	ScopeLocal  Scope = "local"
)

var reasoningValues = []string{"fast", "standard", "medium", "thorough", "maximum"}
var severityValues = []string{"", "critical", "high", "medium", "low"}

func scopes(all ...Scope) []Scope { return all }

// Spec describes one allowlisted setting. Bounds are Dashboard safety policy:
// they are intentionally narrower than upstream's generic parsers.
type Spec struct {
	Key        string      `json:"key"`
	ValueType  SettingType `json:"valueType"`
	Scopes     []Scope     `json:"scopes"`
	EnumValues []string    `json:"enumValues,omitempty"`
	Min        *int64      `json:"min,omitempty"`
	Max        *int64      `json:"max,omitempty"`
}

func intPtr(v int64) *int64 { return &v }

// Settings returns the v1 editable allowlist in stable display order. Keys
// that could weaken safety boundaries (allow_unsafe_agents,
// disable_codex_sandbox, server_addr, *_cmd overrides, credentials) or rewrite
// review orchestration (panels, subagents, hooks, ACP) are deliberately absent.
func Settings() []Spec {
	return []Spec{
		{Key: "max_workers", ValueType: TypeInteger, Scopes: scopes(ScopeGlobal), Min: intPtr(1), Max: intPtr(32)},
		{Key: "review_context_count", ValueType: TypeInteger, Scopes: scopes(ScopeGlobal, ScopeLocal), Min: intPtr(0), Max: intPtr(20)},
		{Key: "auto_close_passing_reviews", ValueType: TypeBoolean, Scopes: scopes(ScopeGlobal, ScopeLocal)},
		{Key: "review_reasoning", ValueType: TypeEnum, Scopes: scopes(ScopeGlobal, ScopeLocal), EnumValues: reasoningValues},
		{Key: "refine_reasoning", ValueType: TypeEnum, Scopes: scopes(ScopeGlobal, ScopeLocal), EnumValues: reasoningValues},
		{Key: "review_min_severity", ValueType: TypeEnum, Scopes: scopes(ScopeGlobal, ScopeLocal), EnumValues: severityValues},
		{Key: "fix_min_severity", ValueType: TypeEnum, Scopes: scopes(ScopeGlobal, ScopeLocal), EnumValues: severityValues},
		{Key: "refine_min_severity", ValueType: TypeEnum, Scopes: scopes(ScopeGlobal, ScopeLocal), EnumValues: severityValues},
	}
}

// Lookup returns the spec for one key.
func Lookup(key string) (Spec, bool) {
	for _, spec := range Settings() {
		if spec.Key == key {
			return spec, true
		}
	}
	return Spec{}, false
}

// Allows reports whether a scope is permitted for a setting.
func (s Spec) Allows(scope Scope) bool {
	for _, allowed := range s.Scopes {
		if allowed == scope {
			return true
		}
	}
	return false
}

// SnapshotDigest derives a stable digest over parsed effective configuration
// rows. It is the change detector that invalidates a prepared preview when
// anything edits RoboRev config out-of-band between preview and set.
func SnapshotDigest(rows []roborev.ConfigRow) string {
	digest := sha256.New()
	for _, row := range rows {
		for _, field := range []string{row.Origin, row.Key, row.Value} {
			_, _ = fmt.Fprintf(digest, "%d:%s\n", len(field), field)
		}
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// CanonicalValue converts a typed JSON request value into the exact CLI string
// RoboRev receives. It fails closed on wrong JSON types, out-of-bounds
// integers, and values outside an enum.
func CanonicalValue(spec Spec, scope Scope, raw json.RawMessage) (string, error) {
	if !spec.Allows(scope) {
		return "", fmt.Errorf("scope %q is not allowed for %q", scope, spec.Key)
	}
	switch spec.ValueType {
	case TypeInteger:
		var number float64
		if err := json.Unmarshal(raw, &number); err != nil {
			return "", fmt.Errorf("%s requires an integer", spec.Key)
		}
		if number != float64(int64(number)) {
			return "", fmt.Errorf("%s requires an integer", spec.Key)
		}
		value := int64(number)
		if spec.Min != nil && value < *spec.Min {
			return "", fmt.Errorf("%s must be >= %d", spec.Key, *spec.Min)
		}
		if spec.Max != nil && value > *spec.Max {
			return "", fmt.Errorf("%s must be <= %d", spec.Key, *spec.Max)
		}
		return strconv.FormatInt(value, 10), nil
	case TypeBoolean:
		var boolean bool
		if err := json.Unmarshal(raw, &boolean); err != nil {
			return "", fmt.Errorf("%s requires true or false", spec.Key)
		}
		return strconv.FormatBool(boolean), nil
	case TypeEnum:
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return "", fmt.Errorf("%s requires one of its allowed values", spec.Key)
		}
		for _, allowed := range spec.EnumValues {
			if text == allowed {
				return text, nil
			}
		}
		return "", fmt.Errorf("%q is not an allowed value for %s", text, spec.Key)
	default:
		return "", fmt.Errorf("unsupported setting type %q", spec.ValueType)
	}
}
