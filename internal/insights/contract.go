// Package insights contains the deliberately small, version-pinned contract
// used by the Guideline Insights workflow.  It has no knowledge of HTTP, UI,
// or RoboRev storage.
package insights

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxReceiptBytes        = 64 << 10
	MaxReceiptString       = 4096
	MaxJobID         int64 = 9007199254740991 // Number.MAX_SAFE_INTEGER
)

type Window string

const (
	Window7d  Window = "7d"
	Window30d Window = "30d"
	Window90d Window = "90d"
)

func (w Window) Valid() bool { return w == Window7d || w == Window30d || w == Window90d }

type BranchScope string

const (
	BranchAll     BranchScope = "all"
	BranchCurrent BranchScope = "current"
)

func (s BranchScope) Valid() bool { return s == BranchAll || s == BranchCurrent }

// Request is the typed, browser-independent Insights launch request. Branch
// is authoritative Git state supplied by the backend and is intentionally
// absent for the all-branches scope.
type Request struct {
	RepoPath    string
	Window      Window
	BranchScope BranchScope
	Branch      string
}

func (r Request) Validate() error {
	if strings.TrimSpace(r.RepoPath) == "" {
		return errors.New("repository path is required")
	}
	if !r.Window.Valid() {
		return fmt.Errorf("unsupported insights window %q", r.Window)
	}
	if !r.BranchScope.Valid() {
		return fmt.Errorf("unsupported insights branch scope %q", r.BranchScope)
	}
	if r.BranchScope == BranchCurrent {
		if !validBoundedString(r.Branch, 256) || strings.TrimSpace(r.Branch) == "" || strings.EqualFold(strings.TrimSpace(r.Branch), "HEAD") {
			return errors.New("current branch is unavailable")
		}
	} else if r.Branch != "" {
		return errors.New("all-branches insights request must not include a branch")
	}
	return nil
}

// Command is a shell-free typed process invocation.
type Command struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
	Dir        string   `json:"dir"`
}

func BuildCommand(executable string, r Request) (Command, error) {
	if err := r.Validate(); err != nil {
		return Command{}, err
	}
	if strings.TrimSpace(executable) == "" {
		return Command{}, errors.New("RoboRev executable is required")
	}
	args := []string{"insights", "--repo", r.RepoPath}
	if r.BranchScope == BranchCurrent {
		args = append(args, "--branch", r.Branch)
	}
	args = append(args, "--since", string(r.Window), "--json")
	return Command{Executable: executable, Args: args, Dir: r.RepoPath}, nil
}

// Preflight is the complete binding checked at preview and immediately
// before launch. The fields are opaque to the browser but are bound into the
// server-side confirmation token.
type Preflight struct {
	Command       Command
	Binding       string
	CLIVersion    string
	DaemonVersion string
}

type Receipt struct {
	JobID   int64
	Agent   string
	Since   time.Time
	Skipped bool
	Reason  string
}

type LaunchResult struct {
	Receipt    *Receipt
	Unverified bool
	Message    string
}

// Adapter is the optional RoboRev client surface. A preflight error is safe
// to report as an error; once Launch is called, all process errors are
// intentionally represented as Unverified because enqueue may have happened.
type Adapter interface {
	InsightsPreflight(context.Context, Request) (Preflight, error)
	Insights(context.Context, Request, Preflight) (LaunchResult, error)
}

func validBoundedString(value string, max int) bool {
	return value != "" && utf8.ValidString(value) && len(value) <= max && !strings.ContainsAny(value, "\x00\r\n")
}

func ParseReceipt(raw []byte) (Receipt, error) {
	if len(raw) == 0 || len(raw) > MaxReceiptBytes {
		return Receipt{}, errors.New("Insights receipt exceeded bounded output")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil || object == nil {
		return Receipt{}, errors.New("malformed Insights receipt")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Receipt{}, errors.New("malformed Insights receipt: trailing JSON")
	}
	// A second map decode cannot detect duplicate keys. Decode the object a
	// second time as tokens and reject duplicates before validating fields.
	if err := rejectDuplicateObjectKeys(raw); err != nil {
		return Receipt{}, err
	}
	for key := range object {
		if key != "job_id" && key != "agent" && key != "since" && key != "skipped" && key != "reason" {
			return Receipt{}, fmt.Errorf("malformed Insights receipt: unknown field %q", key)
		}
	}
	_, hasJob := object["job_id"]
	_, hasAgent := object["agent"]
	_, hasSkipped := object["skipped"]
	_, hasReason := object["reason"]
	_, hasSince := object["since"]
	if !hasSince {
		return Receipt{}, errors.New("malformed Insights receipt: since is required")
	}
	var since string
	if err := json.Unmarshal(object["since"], &since); err != nil || !validBoundedString(since, MaxReceiptString) {
		return Receipt{}, errors.New("malformed Insights receipt: invalid since")
	}
	parsedSince, err := time.Parse(time.RFC3339, since)
	if err != nil {
		return Receipt{}, errors.New("malformed Insights receipt: since is not RFC3339")
	}
	if hasSkipped {
		if hasJob || hasAgent || hasReason == false {
			return Receipt{}, errors.New("malformed Insights skip receipt")
		}
		var skipped bool
		if err := json.Unmarshal(object["skipped"], &skipped); err != nil || !skipped {
			return Receipt{}, errors.New("malformed Insights receipt: skipped must be true")
		}
		var reason string
		if err := json.Unmarshal(object["reason"], &reason); err != nil || !validBoundedString(reason, MaxReceiptString) || strings.TrimSpace(reason) == "" {
			return Receipt{}, errors.New("malformed Insights receipt: reason is required")
		}
		return Receipt{Since: parsedSince, Skipped: true, Reason: reason}, nil
	}
	if !hasJob || !hasAgent || hasReason {
		return Receipt{}, errors.New("malformed Insights success receipt")
	}
	var jobNumber json.Number
	if err := json.Unmarshal(object["job_id"], &jobNumber); err != nil {
		return Receipt{}, errors.New("malformed Insights receipt: invalid job_id")
	}
	jobID, err := strconv.ParseInt(string(jobNumber), 10, 64)
	if err != nil || jobID <= 0 || jobID > MaxJobID {
		return Receipt{}, errors.New("malformed Insights receipt: job_id is not JS-safe")
	}
	var agent string
	if err := json.Unmarshal(object["agent"], &agent); err != nil || !validBoundedString(agent, MaxReceiptString) || strings.TrimSpace(agent) == "" {
		return Receipt{}, errors.New("malformed Insights receipt: agent is required")
	}
	return Receipt{JobID: jobID, Agent: agent, Since: parsedSince}, nil
}

func rejectDuplicateObjectKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return errors.New("malformed Insights receipt")
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return errors.New("malformed Insights receipt: object required")
	}
	seen := map[string]bool{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok {
			return errors.New("malformed Insights receipt")
		}
		if seen[key] {
			return fmt.Errorf("malformed Insights receipt: duplicate field %q", key)
		}
		seen[key] = true
		var value any
		if err := decoder.Decode(&value); err != nil {
			return errors.New("malformed Insights receipt")
		}
	}
	if _, err := decoder.Token(); err != nil {
		return errors.New("malformed Insights receipt")
	}
	return nil
}
