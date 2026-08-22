// Package stats contains the bounded, machine-readable DTOs returned by the
// RoboRev v0.64 summary and cost commands.  This package intentionally has no
// dependency on the RoboRev process adapter so parsers can be tested without
// starting an executable.
package stats

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	SummaryOutputLimit = 256 << 10
	CostOutputLimit    = 64 << 10

	maxAgents      = 128
	maxJobTypes    = 128
	maxRepos       = 256
	maxErrorKeys   = 256
	maxStringBytes = 32 << 10
	maxShortString = 256
	maxSchemaBytes = 1024
	maxCount       = int64(1_000_000_000)
	maxErrorCount  = int64(1_000_000_000)
)

var (
	ErrMalformedSummary = errors.New("malformed RoboRev summary response")
	ErrMalformedCost    = errors.New("malformed RoboRev cost response")
)

// Summary is the exact v0.64 storage.Summary shape used by `summary --json`.
type Summary struct {
	Schema   string    `json:"$schema,omitempty"`
	Since    time.Time `json:"since"`
	RepoPath string    `json:"repo_path,omitempty"`
	Branch   string    `json:"branch,omitempty"`
	Overview Overview  `json:"overview"`
	Verdicts Verdicts  `json:"verdicts"`
	Agents   []Agent   `json:"agents"`
	Duration Duration  `json:"duration"`
	JobTypes []JobType `json:"job_types"`
	Failures Failures  `json:"failures"`
	Repos    []Repo    `json:"repos,omitempty"`
	Cost     Cost      `json:"cost"`
}

type Overview struct {
	Total    int `json:"total"`
	Queued   int `json:"queued"`
	Running  int `json:"running"`
	Done     int `json:"done"`
	Failed   int `json:"failed"`
	Canceled int `json:"canceled"`
	Applied  int `json:"applied"`
	Rebased  int `json:"rebased"`
}

type Verdicts struct {
	Total          int     `json:"total"`
	Passed         int     `json:"passed"`
	Failed         int     `json:"failed"`
	Addressed      int     `json:"addressed"`
	PassRate       float64 `json:"pass_rate"`
	ResolutionRate float64 `json:"resolution_rate"`
}

type Agent struct {
	Agent      string  `json:"agent"`
	Total      int     `json:"total"`
	Passed     int     `json:"passed"`
	Failed     int     `json:"failed"`
	Errors     int     `json:"errors"`
	PassRate   float64 `json:"pass_rate"`
	MedianSecs float64 `json:"median_duration_secs"`
}

type Duration struct {
	ReviewP50 float64 `json:"review_p50_secs"`
	ReviewP90 float64 `json:"review_p90_secs"`
	ReviewP99 float64 `json:"review_p99_secs"`
	QueueP50  float64 `json:"queue_p50_secs"`
	QueueP90  float64 `json:"queue_p90_secs"`
	QueueP99  float64 `json:"queue_p99_secs"`
}

type JobType struct {
	Type    string `json:"type"`
	Count   int    `json:"count"`
	Applied int    `json:"applied,omitempty"`
	Rebased int    `json:"rebased,omitempty"`
}

type Failures struct {
	Total   int            `json:"total"`
	Retries int            `json:"retries"`
	Errors  map[string]int `json:"errors"`
}

type Repo struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Total     int    `json:"total"`
	Passed    int    `json:"passed"`
	Failed    int    `json:"failed"`
	Addressed int    `json:"addressed"`
}

type Cost struct {
	Schema       string  `json:"$schema,omitempty"`
	TotalUSD     float64 `json:"total_usd"`
	JobsWithCost int     `json:"jobs_with_cost"`
	JobsTotal    int     `json:"jobs_total"`
	Complete     bool    `json:"complete"`
}

// CostAggregate is kept as an alias for callers that use the upstream name.
type CostAggregate = Cost

func ParseSummary(output string) (Summary, error) {
	if len(output) == 0 || len(output) > SummaryOutputLimit {
		return Summary{}, fmt.Errorf("%w: output exceeds %d bytes", ErrMalformedSummary, SummaryOutputLimit)
	}
	if !utf8.ValidString(output) {
		return Summary{}, fmt.Errorf("%w: output is not valid UTF-8", ErrMalformedSummary)
	}
	value, err := parseOne(output)
	if err != nil {
		return Summary{}, fmt.Errorf("%w: %v", ErrMalformedSummary, err)
	}
	root, ok := value.(map[string]any)
	if !ok {
		return Summary{}, fmt.Errorf("%w: top-level value must be an object", ErrMalformedSummary)
	}
	if err := validateSummaryShape(root); err != nil {
		return Summary{}, fmt.Errorf("%w: %v", ErrMalformedSummary, err)
	}
	encoded, _ := json.Marshal(root)
	var summary Summary
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&summary); err != nil {
		return Summary{}, fmt.Errorf("%w: %v", ErrMalformedSummary, err)
	}
	if summary.Agents == nil {
		summary.Agents = []Agent{}
	}
	if summary.JobTypes == nil {
		summary.JobTypes = []JobType{}
	}
	if summary.Repos == nil {
		summary.Repos = []Repo{}
	}
	if summary.Failures.Errors == nil {
		summary.Failures.Errors = map[string]int{}
	}
	if err := validateSummaryValues(summary); err != nil {
		return Summary{}, fmt.Errorf("%w: %v", ErrMalformedSummary, err)
	}
	return summary, nil
}

// ParseSummaryJSON is an explicit alias useful to adapters and tests.
func ParseSummaryJSON(output string) (Summary, error) { return ParseSummary(output) }

func ParseCost(output string) (Cost, error) {
	if len(output) == 0 || len(output) > CostOutputLimit {
		return Cost{}, fmt.Errorf("%w: output exceeds %d bytes", ErrMalformedCost, CostOutputLimit)
	}
	if !utf8.ValidString(output) {
		return Cost{}, fmt.Errorf("%w: output is not valid UTF-8", ErrMalformedCost)
	}
	value, err := parseOne(output)
	if err != nil {
		return Cost{}, fmt.Errorf("%w: %v", ErrMalformedCost, err)
	}
	root, ok := value.(map[string]any)
	if !ok {
		return Cost{}, fmt.Errorf("%w: top-level value must be an object", ErrMalformedCost)
	}
	if err := requireObjectFields(root, map[string]string{
		"total_usd": "number", "jobs_with_cost": "number", "jobs_total": "number", "complete": "bool",
	}); err != nil {
		return Cost{}, fmt.Errorf("%w: %v", ErrMalformedCost, err)
	}
	if err := requireOptionalString(root, "$schema", maxSchemaBytes); err != nil {
		return Cost{}, fmt.Errorf("%w: %v", ErrMalformedCost, err)
	}
	encoded, _ := json.Marshal(root)
	var cost Cost
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cost); err != nil {
		return Cost{}, fmt.Errorf("%w: %v", ErrMalformedCost, err)
	}
	if err := validateCostValues(cost); err != nil {
		return Cost{}, fmt.Errorf("%w: %v", ErrMalformedCost, err)
	}
	return cost, nil
}

func ParseCostJSON(output string) (Cost, error) { return ParseCost(output) }

// parseOne detects duplicate object keys in addition to rejecting malformed or
// trailing JSON. encoding/json's DisallowUnknownFields alone does not reject
// duplicate keys, which is unsafe for an external subprocess contract.
func parseOne(output string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(output))
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
					return nil, fmt.Errorf("duplicate object key %q", key)
				}
				child, err := parseValue(decoder)
				if err != nil {
					return nil, err
				}
				object[key] = child
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return object, nil
		case '[':
			array := make([]any, 0)
			for decoder.More() {
				child, err := parseValue(decoder)
				if err != nil {
					return nil, err
				}
				array = append(array, child)
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return array, nil
		default:
			return nil, errors.New("unexpected JSON delimiter")
		}
	default:
		return value, nil
	}
}

func validateSummaryShape(root map[string]any) error {
	if err := requireOptionalString(root, "$schema", maxSchemaBytes); err != nil {
		return err
	}
	if err := requireField(root, "since", "string"); err != nil {
		return err
	}
	if since, _ := root["since"].(string); since == "" {
		return errors.New("since is empty")
	}
	if err := requireOptionalString(root, "repo_path", maxStringBytes); err != nil {
		return err
	}
	if err := requireOptionalString(root, "branch", maxShortString); err != nil {
		return err
	}
	for _, field := range []string{"overview", "verdicts", "duration", "failures", "cost"} {
		if err := requireField(root, field, "object"); err != nil {
			return err
		}
	}
	if err := requireObjectFields(root["overview"].(map[string]any), map[string]string{
		"total": "number", "queued": "number", "running": "number", "done": "number", "failed": "number", "canceled": "number", "applied": "number", "rebased": "number",
	}); err != nil {
		return fmt.Errorf("overview: %w", err)
	}
	if err := requireObjectFields(root["verdicts"].(map[string]any), map[string]string{
		"total": "number", "passed": "number", "failed": "number", "addressed": "number", "pass_rate": "number", "resolution_rate": "number",
	}); err != nil {
		return fmt.Errorf("verdicts: %w", err)
	}
	if err := requireObjectFields(root["duration"].(map[string]any), map[string]string{
		"review_p50_secs": "number", "review_p90_secs": "number", "review_p99_secs": "number", "queue_p50_secs": "number", "queue_p90_secs": "number", "queue_p99_secs": "number",
	}); err != nil {
		return fmt.Errorf("duration: %w", err)
	}
	if err := requireObjectFields(root["failures"].(map[string]any), map[string]string{"total": "number", "retries": "number", "errors": "object"}); err != nil {
		return fmt.Errorf("failures: %w", err)
	}
	if errorsObject, ok := root["failures"].(map[string]any)["errors"].(map[string]any); ok {
		if len(errorsObject) > maxErrorKeys {
			return errors.New("failures.errors has too many categories")
		}
		for key, value := range errorsObject {
			if err := validateText(key, maxShortString, "failure category"); err != nil {
				return err
			}
			if _, ok := value.(json.Number); !ok {
				return fmt.Errorf("failure category %q must be a number", key)
			}
		}
	}
	if err := requireObjectFields(root["cost"].(map[string]any), map[string]string{"total_usd": "number", "jobs_with_cost": "number", "jobs_total": "number", "complete": "bool"}); err != nil {
		return fmt.Errorf("cost: %w", err)
	}
	for _, field := range []string{"agents", "job_types"} {
		value, exists := root[field]
		if !exists {
			return fmt.Errorf("%s is required", field)
		}
		if value != nil {
			if _, ok := value.([]any); !ok {
				return fmt.Errorf("%s must be an array or null", field)
			}
		}
	}
	for _, field := range []string{"repos"} {
		if value, exists := root[field]; exists && value != nil {
			if _, ok := value.([]any); !ok {
				return fmt.Errorf("%s must be an array or null", field)
			}
		}
	}
	if agents, ok := root["agents"].([]any); ok {
		if len(agents) > maxAgents {
			return errors.New("agents array is too large")
		}
		for index, item := range agents {
			object, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("agents[%d] must be an object", index)
			}
			if err := validateAgentShape(object); err != nil {
				return fmt.Errorf("agents[%d]: %w", index, err)
			}
		}
	}
	if jobTypes, ok := root["job_types"].([]any); ok {
		if len(jobTypes) > maxJobTypes {
			return errors.New("job_types array is too large")
		}
		for index, item := range jobTypes {
			object, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("job_types[%d] must be an object", index)
			}
			if err := validateJobTypeShape(object); err != nil {
				return fmt.Errorf("job_types[%d]: %w", index, err)
			}
		}
	}
	if repos, ok := root["repos"].([]any); ok {
		if len(repos) > maxRepos {
			return errors.New("repos array is too large")
		}
		for index, item := range repos {
			object, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("repos[%d] must be an object", index)
			}
			if err := requireObjectFields(object, map[string]string{"name": "string", "path": "string", "total": "number", "passed": "number", "failed": "number", "addressed": "number"}); err != nil {
				return fmt.Errorf("repos[%d]: %w", index, err)
			}
		}
	}
	return nil
}

func validateAgentShape(object map[string]any) error {
	return requireObjectFields(object, map[string]string{"agent": "string", "total": "number", "passed": "number", "failed": "number", "errors": "number", "pass_rate": "number", "median_duration_secs": "number"})
}

func validateJobTypeShape(object map[string]any) error {
	if err := requireObjectFields(object, map[string]string{"type": "string", "count": "number"}); err != nil {
		return err
	}
	for _, field := range []string{"applied", "rebased"} {
		if err := requireOptionalNumber(object, field); err != nil {
			return err
		}
	}
	return nil
}

func requireObjectFields(object map[string]any, fields map[string]string) error {
	for field, kind := range fields {
		if err := requireField(object, field, kind); err != nil {
			return err
		}
	}
	return nil
}

func requireField(object map[string]any, field, kind string) error {
	value, ok := object[field]
	if !ok || value == nil {
		return fmt.Errorf("%s is required", field)
	}
	switch kind {
	case "number":
		if _, ok := value.(json.Number); !ok {
			return fmt.Errorf("%s must be a number", field)
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s must be a string", field)
		}
	case "bool":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s must be a boolean", field)
		}
	case "object":
		if _, ok := value.(map[string]any); !ok {
			return fmt.Errorf("%s must be an object", field)
		}
	default:
		return fmt.Errorf("unsupported field kind %q", kind)
	}
	return nil
}

func requireOptionalString(object map[string]any, field string, maxBytes int) error {
	value, exists := object[field]
	if !exists {
		return nil
	}
	if value == nil {
		return fmt.Errorf("%s must be a string", field)
	}
	text, ok := value.(string)
	if !ok {
		return fmt.Errorf("%s must be a string", field)
	}
	return validateText(text, maxBytes, field)
}

func requireOptionalNumber(object map[string]any, field string) error {
	value, exists := object[field]
	if !exists {
		return nil
	}
	if value == nil {
		return fmt.Errorf("%s must be a number", field)
	}
	if _, ok := value.(json.Number); !ok {
		return fmt.Errorf("%s must be a number", field)
	}
	return nil
}

func validateSummaryValues(summary Summary) error {
	if summary.Since.IsZero() {
		return errors.New("since is not a valid timestamp")
	}
	if err := validateText(summary.Schema, maxSchemaBytes, "$schema"); err != nil {
		return err
	}
	if err := validateText(summary.RepoPath, maxStringBytes, "repo_path"); err != nil {
		return err
	}
	if err := validateText(summary.Branch, maxShortString, "branch"); err != nil {
		return err
	}
	if err := validateNonnegativeInts("overview", []int{summary.Overview.Total, summary.Overview.Queued, summary.Overview.Running, summary.Overview.Done, summary.Overview.Failed, summary.Overview.Canceled, summary.Overview.Applied, summary.Overview.Rebased}); err != nil {
		return err
	}
	for name, value := range map[string]int{"queued": summary.Overview.Queued, "running": summary.Overview.Running, "done": summary.Overview.Done, "failed": summary.Overview.Failed, "canceled": summary.Overview.Canceled, "applied": summary.Overview.Applied, "rebased": summary.Overview.Rebased} {
		if value > summary.Overview.Total {
			return fmt.Errorf("overview.%s exceeds total", name)
		}
	}
	if summary.Overview.Queued+summary.Overview.Running+summary.Overview.Done+summary.Overview.Failed+summary.Overview.Canceled+summary.Overview.Applied+summary.Overview.Rebased > summary.Overview.Total {
		return errors.New("overview status counts exceed total")
	}
	if err := validateNonnegativeInts("verdicts", []int{summary.Verdicts.Total, summary.Verdicts.Passed, summary.Verdicts.Failed, summary.Verdicts.Addressed}); err != nil {
		return err
	}
	if summary.Verdicts.Passed+summary.Verdicts.Failed != summary.Verdicts.Total {
		return errors.New("verdicts passed plus failed must equal total")
	}
	if summary.Verdicts.Addressed > summary.Verdicts.Failed {
		return errors.New("verdicts addressed exceeds failed")
	}
	if err := validateRate(summary.Verdicts.PassRate, "verdicts.pass_rate"); err != nil {
		return err
	}
	if err := validateRate(summary.Verdicts.ResolutionRate, "verdicts.resolution_rate"); err != nil {
		return err
	}
	if err := validateConsistentRate(summary.Verdicts.PassRate, summary.Verdicts.Passed, summary.Verdicts.Total, "verdicts.pass_rate"); err != nil {
		return err
	}
	if err := validateConsistentRate(summary.Verdicts.ResolutionRate, summary.Verdicts.Addressed, summary.Verdicts.Failed, "verdicts.resolution_rate"); err != nil {
		return err
	}
	for index, agent := range summary.Agents {
		if err := validateText(agent.Agent, maxShortString, fmt.Sprintf("agents[%d].agent", index)); err != nil {
			return err
		}
		if err := validateNonnegativeInts(fmt.Sprintf("agents[%d]", index), []int{agent.Total, agent.Passed, agent.Failed, agent.Errors}); err != nil {
			return err
		}
		if agent.Passed+agent.Failed > agent.Total {
			return fmt.Errorf("agents[%d] passed plus failed exceeds total", index)
		}
		if err := validateRate(agent.PassRate, fmt.Sprintf("agents[%d].pass_rate", index)); err != nil {
			return err
		}
		if err := validateConsistentRate(agent.PassRate, agent.Passed, agent.Passed+agent.Failed, fmt.Sprintf("agents[%d].pass_rate", index)); err != nil {
			return err
		}
		if err := validateFiniteNonnegative(agent.MedianSecs, fmt.Sprintf("agents[%d].median_duration_secs", index)); err != nil {
			return err
		}
	}
	for name, value := range map[string]float64{"duration.review_p50_secs": summary.Duration.ReviewP50, "duration.review_p90_secs": summary.Duration.ReviewP90, "duration.review_p99_secs": summary.Duration.ReviewP99, "duration.queue_p50_secs": summary.Duration.QueueP50, "duration.queue_p90_secs": summary.Duration.QueueP90, "duration.queue_p99_secs": summary.Duration.QueueP99} {
		if err := validateFiniteNonnegative(value, name); err != nil {
			return err
		}
	}
	if summary.Duration.ReviewP50 > summary.Duration.ReviewP90 || summary.Duration.ReviewP90 > summary.Duration.ReviewP99 || summary.Duration.QueueP50 > summary.Duration.QueueP90 || summary.Duration.QueueP90 > summary.Duration.QueueP99 {
		return errors.New("duration percentiles are not monotonic")
	}
	for index, jobType := range summary.JobTypes {
		if err := validateText(jobType.Type, maxShortString, fmt.Sprintf("job_types[%d].type", index)); err != nil {
			return err
		}
		if err := validateNonnegativeInts(fmt.Sprintf("job_types[%d]", index), []int{jobType.Count, jobType.Applied, jobType.Rebased}); err != nil {
			return err
		}
		if jobType.Applied+jobType.Rebased > jobType.Count {
			return fmt.Errorf("job_types[%d] applied plus rebased exceeds count", index)
		}
	}
	if err := validateNonnegativeInts("failures", []int{summary.Failures.Total, summary.Failures.Retries}); err != nil {
		return err
	}
	if len(summary.Failures.Errors) > maxErrorKeys {
		return errors.New("failures.errors has too many categories")
	}
	for key, value := range summary.Failures.Errors {
		if err := validateText(key, maxShortString, "failure category"); err != nil {
			return err
		}
		if err := validateNonnegativeInts("failure category", []int{value}); err != nil {
			return err
		}
	}
	var failureCategoryTotal int
	for _, value := range summary.Failures.Errors {
		failureCategoryTotal += value
	}
	if failureCategoryTotal > summary.Failures.Total {
		return errors.New("failure category counts exceed failures.total")
	}
	for index, repo := range summary.Repos {
		if err := validateText(repo.Name, maxShortString, fmt.Sprintf("repos[%d].name", index)); err != nil {
			return err
		}
		if err := validateText(repo.Path, maxStringBytes, fmt.Sprintf("repos[%d].path", index)); err != nil {
			return err
		}
		if err := validateNonnegativeInts(fmt.Sprintf("repos[%d]", index), []int{repo.Total, repo.Passed, repo.Failed, repo.Addressed}); err != nil {
			return err
		}
		if repo.Passed+repo.Failed > repo.Total {
			return fmt.Errorf("repos[%d] passed plus failed exceeds total", index)
		}
		if repo.Addressed > repo.Failed {
			return fmt.Errorf("repos[%d] addressed exceeds failed", index)
		}
	}
	return validateCostValues(summary.Cost)
}

func validateCostValues(cost Cost) error {
	if err := validateText(cost.Schema, maxSchemaBytes, "$schema"); err != nil {
		return err
	}
	if math.IsNaN(cost.TotalUSD) || math.IsInf(cost.TotalUSD, 0) || cost.TotalUSD < 0 {
		return errors.New("total_usd must be finite and nonnegative")
	}
	if err := validateNonnegativeInts("cost", []int{cost.JobsWithCost, cost.JobsTotal}); err != nil {
		return err
	}
	if cost.JobsWithCost > cost.JobsTotal {
		return errors.New("jobs_with_cost exceeds jobs_total")
	}
	if cost.JobsTotal == 0 && cost.Complete {
		return errors.New("complete cost requires eligible jobs")
	}
	if cost.Complete && cost.JobsWithCost != cost.JobsTotal {
		return errors.New("complete cost requires full coverage")
	}
	if cost.Complete != (cost.JobsTotal > 0 && cost.JobsWithCost == cost.JobsTotal) {
		return errors.New("complete cost flag does not match coverage")
	}
	return nil
}

func validateRate(value float64, field string) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		return fmt.Errorf("%s must be finite and between 0 and 1", field)
	}
	return nil
}

const rateTolerance = 1e-9

func validateConsistentRate(actual float64, numerator, denominator int, field string) error {
	expected := 0.0
	if denominator > 0 {
		expected = float64(numerator) / float64(denominator)
	}
	if math.Abs(actual-expected) > rateTolerance {
		return fmt.Errorf("%s does not match its count-derived rate", field)
	}
	return nil
}

func validateFiniteNonnegative(value float64, field string) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return fmt.Errorf("%s must be finite and nonnegative", field)
	}
	return nil
}

func validateNonnegativeInts(field string, values []int) error {
	for _, value := range values {
		if value < 0 || int64(value) > maxCount {
			return fmt.Errorf("%s contains an invalid count", field)
		}
	}
	return nil
}

func validateText(value string, maxBytes int, field string) error {
	if len(value) > maxBytes {
		return fmt.Errorf("%s exceeds %d bytes", field, maxBytes)
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return fmt.Errorf("%s contains control characters", field)
		}
	}
	return nil
}
