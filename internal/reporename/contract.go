// Package reporename contains the bounded, version-pinned contract for the
// RoboRev repository display-name rename operation.
package reporename

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxNameRunes = 128

var (
	ErrInvalidName = errors.New("invalid repository name")
	ErrSameName    = errors.New("repository already has that name")
)

type Request struct {
	Path        string
	CurrentName string // optional at command-build level; used for final revalidation
	NewName     string
}

// Command is a shell-free invocation. Dir is intentionally empty: rename is
// a RoboRev DB metadata operation and does not require a checkout.
type Command struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
	Dir        string   `json:"dir"`
}

type Preflight struct {
	Command       Command
	Binding       string
	CLIVersion    string
	DaemonVersion string
}

func NormalizeName(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("%w: name must be valid UTF-8", ErrInvalidName)
	}
	name := strings.TrimSpace(value)
	if name == "" {
		return "", fmt.Errorf("%w: name is required", ErrInvalidName)
	}
	if utf8.RuneCountInString(name) > MaxNameRunes {
		return "", fmt.Errorf("%w: name exceeds %d characters", ErrInvalidName, MaxNameRunes)
	}
	if strings.HasPrefix(name, "-") {
		return "", fmt.Errorf("%w: names beginning with '-' are not supported", ErrInvalidName)
	}
	for _, r := range name {
		if r == '\x00' || r == '\r' || r == '\n' || unicode.IsControl(r) {
			return "", fmt.Errorf("%w: name contains a control character", ErrInvalidName)
		}
	}
	return name, nil
}

func ValidateRequest(request Request) (Request, error) {
	if strings.TrimSpace(request.Path) == "" {
		return Request{}, errors.New("repository path is required")
	}
	name, err := NormalizeName(request.NewName)
	if err != nil {
		return Request{}, err
	}
	return Request{Path: request.Path, CurrentName: request.CurrentName, NewName: name}, nil
}

func BuildCommand(executable string, request Request) (Command, error) {
	if strings.TrimSpace(executable) == "" {
		return Command{}, errors.New("RoboRev executable is required")
	}
	request, err := ValidateRequest(request)
	if err != nil {
		return Command{}, err
	}
	return Command{Executable: executable, Args: []string{"repo", "rename", request.Path, request.NewName}}, nil
}
