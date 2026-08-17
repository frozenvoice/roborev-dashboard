package roborev

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var allowedPanels = map[string]bool{"dashboard_cross": true, "none": true}

func validateJobID(id int64) error {
	if id <= 0 {
		return errors.New("job ID must be positive")
	}
	return nil
}

func validatePanel(panel string) error {
	if panel == "" {
		return nil
	}
	if !allowedPanels[panel] {
		return fmt.Errorf("unsupported panel %q", panel)
	}
	return nil
}

func ListArgs(repo string, limit int) []string {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	return []string{"list", "--json", "--repo", repo, "--limit", strconv.Itoa(limit)}
}

func ShowArgs(id int64) ([]string, error) {
	if err := validateJobID(id); err != nil {
		return nil, err
	}
	return []string{"show", "--job", strconv.FormatInt(id, 10), "--json"}, nil
}

func PromptArgs(id int64) ([]string, error) {
	if err := validateJobID(id); err != nil {
		return nil, err
	}
	return []string{"show", "--job", strconv.FormatInt(id, 10), "--prompt"}, nil
}

func LogArgs(id int64) ([]string, error) {
	if err := validateJobID(id); err != nil {
		return nil, err
	}
	return []string{"log", "--raw", strconv.FormatInt(id, 10)}, nil
}

func CommentArgs(id int64, comment string) ([]string, error) {
	if err := validateJobID(id); err != nil {
		return nil, err
	}
	if strings.TrimSpace(comment) == "" {
		return nil, errors.New("comment is required")
	}
	if len(comment) > 100_000 {
		return nil, errors.New("comment exceeds 100000 characters")
	}
	return []string{"comment", "--job", strconv.FormatInt(id, 10), "--message", comment, "--commenter", "roborev-dashboard"}, nil
}

func CloseArgs(id int64, reopen bool) ([]string, error) {
	if err := validateJobID(id); err != nil {
		return nil, err
	}
	args := []string{"close", strconv.FormatInt(id, 10)}
	if reopen {
		args = append(args, "--reopen")
	}
	return args, nil
}

func CancelArgs(id int64) ([]string, error) {
	if err := validateJobID(id); err != nil {
		return nil, err
	}
	return []string{"cancel", strconv.FormatInt(id, 10)}, nil
}

func ReviewPreview(binary string, request ReviewRequest) (CommandPreview, error) {
	if request.RepoPath == "" || request.GitRef == "" {
		return CommandPreview{}, errors.New("repository and git ref are required")
	}
	if err := validatePanel(request.Panel); err != nil {
		return CommandPreview{}, err
	}
	args := []string{"review", request.GitRef, "--repo", request.RepoPath}
	if request.Panel != "" {
		args = append(args, "--panel", request.Panel)
	}
	return CommandPreview{Executable: binary, Args: args}, nil
}
