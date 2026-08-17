package roborev

import "encoding/json"

type Installation struct {
	Installed    bool            `json:"installed"`
	Binary       string          `json:"binary,omitempty"`
	Version      string          `json:"version,omitempty"`
	Status       json.RawMessage `json:"status,omitempty"`
	Error        string          `json:"error,omitempty"`
	Capabilities Capabilities    `json:"capabilities"`
}

type Capabilities struct {
	ReviewEnqueueAPI bool `json:"reviewEnqueueApi"`
	FrozenRerunAPI   bool `json:"frozenRerunApi"`
	JobListAPI       bool `json:"jobListApi"`
	PanelReview      bool `json:"panelReview"`
	PanelMembersAPI  bool `json:"panelMembersApi"`
	PromptLazyLoad   bool `json:"promptLazyLoad"`
	RawLog           bool `json:"rawLog"`
}

type JobMetadata struct {
	ID           int64  `json:"id"`
	RepoPath     string `json:"repo_path"`
	PanelRunUUID string `json:"panel_run_uuid,omitempty"`
}

type ReviewRequest struct {
	RepoPath string `json:"repoPath"`
	GitRef   string `json:"gitRef"`
	Branch   string `json:"branch,omitempty"`
	Panel    string `json:"panel,omitempty"`
}

type CommentRequest struct {
	JobID   int64  `json:"jobId"`
	Comment string `json:"comment"`
}
