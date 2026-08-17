package roborev

import (
	"errors"
	"strconv"
	"strings"
	"unicode"
)

func validateJobID(id int64) error {
	if id <= 0 {
		return errors.New("job ID must be positive")
	}
	return nil
}

func validatePanel(panel string) error {
	if strings.TrimSpace(panel) == "" {
		return errors.New("panel is required")
	}
	if len(panel) > 128 {
		return errors.New("panel exceeds 128 characters")
	}
	for _, char := range panel {
		if unicode.IsControl(char) {
			return errors.New("panel contains control characters")
		}
	}
	return nil
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

func FixArgs(id int64, agent string) ([]string, error) {
	if err := validateJobID(id); err != nil {
		return nil, err
	}
	if agent != "codex" {
		return nil, errors.New("fix agent is not allowed")
	}
	return []string{"fix", "--agent", agent, "--quiet", strconv.FormatInt(id, 10)}, nil
}

func ValidateReviewRequest(request ReviewRequest) error {
	if strings.TrimSpace(request.RepoPath) == "" || strings.TrimSpace(request.GitRef) == "" {
		return errors.New("repository and git ref are required")
	}
	if err := validatePanel(request.Panel); err != nil {
		return err
	}
	return nil
}
