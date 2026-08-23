package roborev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/froze/roborev-dashboard/internal/insights"
	proc "github.com/froze/roborev-dashboard/internal/process"
	"github.com/froze/roborev-dashboard/internal/roborevcompat"
)

const insightsLaunchTimeout = 35 * time.Second

// InsightsPreflight performs fresh, typed validation. It intentionally does
// not reuse Installation's cached/version-projection result: preview and the
// launch boundary must bind the exact executable generation and daemon
// version immediately before the subprocess is entered.
func (c *Client) InsightsPreflight(ctx context.Context, request insights.Request) (insights.Preflight, error) {
	if c == nil {
		return insights.Preflight{}, errors.New("RoboRev client is unavailable")
	}
	if err := request.Validate(); err != nil {
		return insights.Preflight{}, err
	}
	binary, _, generation := c.snapshot()
	if strings.TrimSpace(binary) == "" {
		return insights.Preflight{}, errors.New("RoboRev executable is not bound")
	}
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() {
		return insights.Preflight{}, errors.New("RoboRev executable binding is unavailable")
	}
	versionResult, err := c.Runner.Run(ctx, proc.Command{Executable: binary, Args: []string{"version", "--json"}, Timeout: 5 * time.Second, OutputLimit: insights.MaxReceiptBytes})
	if err != nil || !versionResult.Started || versionResult.StdoutTruncated || versionResult.StderrTruncated {
		if err == nil {
			err = errors.New("version probe failed")
		}
		return insights.Preflight{}, fmt.Errorf("RoboRev version preflight failed: %w", err)
	}
	cliVersion, err := decodeExactVersion(versionResult.Stdout)
	if err != nil {
		return insights.Preflight{}, err
	}
	if !roborevcompat.IsSupportedVersion(cliVersion) {
		return insights.Preflight{}, fmt.Errorf("RoboRev %s does not support Guideline Insights", cliVersion)
	}
	health, err := c.healthStatus(ctx)
	if err != nil {
		return insights.Preflight{}, fmt.Errorf("RoboRev daemon health preflight failed: %w", err)
	}
	if !health.Healthy {
		return insights.Preflight{}, ErrDaemonUnhealthy
	}
	if !roborevcompat.IsMatchingSupportedVersion(cliVersion, health.Version) {
		return insights.Preflight{}, fmt.Errorf("RoboRev CLI/daemon version mismatch: %s/%s", cliVersion, health.Version)
	}
	if !c.generationCurrent(generation) {
		return insights.Preflight{}, ErrBindingChanged
	}
	command, err := insights.BuildCommand(binary, request)
	if err != nil {
		return insights.Preflight{}, err
	}
	return insights.Preflight{Command: command, Binding: fmt.Sprintf("%d:%s", generation, binary), CLIVersion: cliVersion, DaemonVersion: health.Version}, nil
}

func decodeExactVersion(raw string) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.DisallowUnknownFields()
	var value struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("malformed RoboRev version response: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return "", errors.New("malformed RoboRev version response: trailing JSON")
	}
	if err := roborevcompat.ValidateVersionIdentity(value.Name, value.Version); err != nil {
		return "", err
	}
	return value.Version, nil
}

// Insights is at-most-once at the Dashboard boundary. It revalidates the
// token-bound preflight and then executes exactly one shell-free process. Any
// error after Run is entered is intentionally returned as Unverified.
func (c *Client) Insights(ctx context.Context, request insights.Request, expected insights.Preflight) (insights.LaunchResult, error) {
	if c == nil {
		return insights.LaunchResult{}, errors.New("RoboRev client is unavailable")
	}
	fresh, err := c.InsightsPreflight(ctx, request)
	if err != nil {
		return insights.LaunchResult{}, err
	}
	if fresh.Binding != expected.Binding || fresh.CLIVersion != expected.CLIVersion || fresh.DaemonVersion != expected.DaemonVersion || fresh.Command.Executable != expected.Command.Executable || !sameArgs(fresh.Command.Args, expected.Command.Args) || fresh.Command.Dir != expected.Command.Dir {
		return insights.LaunchResult{}, ErrBindingChanged
	}
	result, runErr := c.Runner.Run(ctx, proc.Command{Executable: fresh.Command.Executable, Args: append([]string(nil), fresh.Command.Args...), Dir: fresh.Command.Dir, Timeout: insightsLaunchTimeout, OutputLimit: insights.MaxReceiptBytes})
	// A process runner that did not start the executable is still an
	// ambiguous boundary from the adapter's perspective: a broken runner must
	// never turn an absent process into a verified queue receipt. Keep the
	// result unverified, just as for any other post-entry launch uncertainty.
	if runErr != nil || !result.Started || result.StdoutTruncated || result.StderrTruncated {
		return insights.LaunchResult{Unverified: true, Message: "Launch outcome could not be verified. A RoboRev Insights job may already have been queued."}, nil
	}
	if fresh.Binding != c.BindingIdentity() {
		return insights.LaunchResult{Unverified: true, Message: "Launch outcome could not be verified because the RoboRev binding changed. A RoboRev Insights job may already have been queued."}, nil
	}
	receipt, parseErr := insights.ParseReceipt([]byte(result.Stdout))
	if parseErr != nil {
		return insights.LaunchResult{Unverified: true, Message: "Launch outcome could not be verified. A RoboRev Insights job may already have been queued."}, nil
	}
	return insights.LaunchResult{Receipt: &receipt}, nil
}

func sameArgs(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
