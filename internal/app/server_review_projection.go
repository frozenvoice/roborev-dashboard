package app

import (
	"bytes"
	"encoding/json"
	"errors"
)

// Review and job payloads contain server-side repository roots and daemon
// execution metadata that are needed for authorization but are not browser
// DTOs. These allowlists are deliberately separate from the RoboRev adapter:
// handlers first validate ownership against the internal payload, then project
// only display data across the browser boundary.
var browserJobFields = map[string]struct{}{
	"id": {}, "job_id": {}, "review_id": {}, "uuid": {}, "job_uuid": {},
	"git_ref": {}, "commit_sha": {}, "branch": {}, "subject": {}, "commit_subject": {},
	"job_type": {}, "agent": {}, "model": {}, "reasoning": {},
	"status": {}, "state": {}, "verdict": {}, "verdict_bool": {},
	"closed": {}, "addressed": {}, "skipped": {},
	"enqueued_at": {}, "started_at": {}, "finished_at": {}, "created_at": {}, "completed_at": {},
	"panel_name": {}, "panel_run_uuid": {}, "panel_role": {}, "panel_member_name": {},
	"panel_member_index": {}, "parent_job_id": {}, "member_job_ids": {}, "duration_ms": {},
	"repo_name": {},
}

var browserReviewFields = map[string]struct{}{
	"id": {}, "job_id": {}, "review_id": {}, "status": {}, "state": {},
	"closed": {}, "addressed": {}, "skipped": {}, "verdict": {}, "verdict_bool": {},
	"git_ref": {}, "commit_sha": {}, "branch": {}, "subject": {}, "commit_subject": {},
	"job_type": {}, "agent": {}, "model": {}, "reasoning": {},
	"panel_name": {}, "panel_run_uuid": {}, "panel_role": {}, "panel_member_name": {},
	"panel_member_index": {}, "parent_job_id": {}, "member_job_ids": {}, "duration_ms": {},
	"enqueued_at": {}, "started_at": {}, "finished_at": {}, "created_at": {}, "completed_at": {},
	"output": {}, "text": {}, "markdown": {},
}

var browserReceiptFields = map[string]struct{}{
	"id": {}, "job_id": {}, "uuid": {}, "status": {}, "skipped": {}, "reason": {},
	"git_ref": {}, "branch": {}, "job_type": {}, "panel_name": {}, "panel_run_uuid": {},
	"panel_role": {}, "member_job_ids": {},
}

func projectReviewListForBrowser(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("review list response is empty")
	}
	if trimmed[0] == '[' {
		return projectReviewArray(trimmed)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &envelope); err != nil || envelope == nil {
		return nil, errors.New("review list response is malformed")
	}
	projected := make(map[string]json.RawMessage, 2)
	found := false
	for _, key := range []string{"jobs", "reviews", "items"} {
		value, ok := envelope[key]
		if !ok {
			continue
		}
		if found {
			return nil, errors.New("review list response has ambiguous collections")
		}
		clean, err := projectReviewArray(value)
		if err != nil {
			return nil, err
		}
		projected[key] = clean
		found = true
	}
	if !found {
		return nil, errors.New("review list response has no job collection")
	}
	if value, ok := envelope["has_more"]; ok {
		var hasMore bool
		if err := json.Unmarshal(value, &hasMore); err != nil {
			return nil, errors.New("review list pagination is malformed")
		}
		projected["has_more"], _ = json.Marshal(hasMore)
	}
	encoded, err := json.Marshal(projected)
	if err != nil {
		return nil, errors.New("review list response could not be projected")
	}
	return encoded, nil
}

func projectReviewArray(raw json.RawMessage) (json.RawMessage, error) {
	var rows []json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, errors.New("review list collection is malformed")
	}
	clean := make([]json.RawMessage, 0, len(rows))
	for _, row := range rows {
		projected, err := projectBrowserObject(row, browserJobFields, false)
		if err != nil {
			return nil, err
		}
		clean = append(clean, projected)
	}
	encoded, err := json.Marshal(clean)
	if err != nil {
		return nil, errors.New("review list collection could not be projected")
	}
	return encoded, nil
}

func projectReviewDetailForBrowser(raw json.RawMessage) (json.RawMessage, error) {
	return projectBrowserObject(raw, browserReviewFields, true)
}

func projectReviewReceiptForBrowser(raw json.RawMessage) (json.RawMessage, error) {
	return projectBrowserObject(raw, browserReceiptFields, false)
}

func projectBrowserObject(raw json.RawMessage, allowed map[string]struct{}, detail bool) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, errors.New("review response is malformed")
	}
	clean := make(map[string]json.RawMessage, len(object))
	for key, value := range object {
		switch key {
		case "job":
			if !detail {
				continue
			}
			nested, err := projectBrowserObject(value, browserJobFields, false)
			if err != nil {
				return nil, err
			}
			clean[key] = nested
		case "review":
			if !detail {
				continue
			}
			nested, err := projectBrowserObject(value, browserReviewFields, true)
			if err != nil {
				return nil, err
			}
			clean[key] = nested
		default:
			if _, ok := allowed[key]; ok {
				if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
					continue
				}
				clean[key] = append(json.RawMessage(nil), value...)
			}
		}
	}
	encoded, err := json.Marshal(clean)
	if err != nil {
		return nil, errors.New("review response could not be projected")
	}
	return encoded, nil
}
