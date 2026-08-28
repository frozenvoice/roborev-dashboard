package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"unicode/utf8"
)

// Review and job payloads contain server-side repository roots and daemon
// execution metadata that are needed for authorization but are not browser
// DTOs. These schemas deliberately validate the value shape as well as the
// field name. A key-only projection would let an upstream object, array, or
// null value cross the boundary under a familiar safe-looking key.
//
// The scalar set follows the fields accepted by web/src/api.ts's review
// normalizers. Values not represented by a browser normalizer are omitted,
// while a value for a represented field with the wrong shape fails closed.
type browserReviewShape uint8

const (
	browserReviewID browserReviewShape = iota + 1
	browserReviewIndex
	browserReviewDuration
	browserReviewBool
	browserReviewVerdictBool
	browserReviewText
	browserReviewOutput
	browserReviewIDs
	browserReviewPayload
)

type browserReviewField struct {
	shape browserReviewShape
}

var browserReviewFields = map[string]browserReviewField{
	"id":                 {shape: browserReviewID},
	"job_id":             {shape: browserReviewID},
	"review_id":          {shape: browserReviewID},
	"uuid":               {shape: browserReviewText},
	"job_uuid":           {shape: browserReviewText},
	"repo_name":          {shape: browserReviewText},
	"git_ref":            {shape: browserReviewText},
	"commit_sha":         {shape: browserReviewText},
	"branch":             {shape: browserReviewText},
	"subject":            {shape: browserReviewText},
	"commit_subject":     {shape: browserReviewText},
	"job_type":           {shape: browserReviewText},
	"agent":              {shape: browserReviewText},
	"model":              {shape: browserReviewText},
	"reasoning":          {shape: browserReviewText},
	"status":             {shape: browserReviewText},
	"state":              {shape: browserReviewText},
	"verdict":            {shape: browserReviewText},
	"enqueued_at":        {shape: browserReviewText},
	"started_at":         {shape: browserReviewText},
	"finished_at":        {shape: browserReviewText},
	"created_at":         {shape: browserReviewText},
	"completed_at":       {shape: browserReviewText},
	"panel_name":         {shape: browserReviewText},
	"panel_run_uuid":     {shape: browserReviewText},
	"panel_role":         {shape: browserReviewText},
	"panel_member_name":  {shape: browserReviewText},
	"verdict_bool":       {shape: browserReviewVerdictBool},
	"closed":             {shape: browserReviewBool},
	"addressed":          {shape: browserReviewBool},
	"skipped":            {shape: browserReviewBool},
	"panel_member_index": {shape: browserReviewIndex},
	"parent_job_id":      {shape: browserReviewID},
	"duration_ms":        {shape: browserReviewDuration},
	"member_job_ids":     {shape: browserReviewIDs},
	"output":             {shape: browserReviewOutput},
	"review":             {shape: browserReviewPayload},
}

// Review detail has a few display-only aliases in addition to the normal job
// shape. The nested job object is the only full recursive object accepted by
// this projection; nested review values use the dedicated review-payload
// schema below.
var browserReviewDetailFields = map[string]browserReviewField{
	"id":                 {shape: browserReviewID},
	"job_id":             {shape: browserReviewID},
	"review_id":          {shape: browserReviewID},
	"git_ref":            {shape: browserReviewText},
	"commit_sha":         {shape: browserReviewText},
	"branch":             {shape: browserReviewText},
	"subject":            {shape: browserReviewText},
	"commit_subject":     {shape: browserReviewText},
	"job_type":           {shape: browserReviewText},
	"agent":              {shape: browserReviewText},
	"model":              {shape: browserReviewText},
	"reasoning":          {shape: browserReviewText},
	"status":             {shape: browserReviewText},
	"state":              {shape: browserReviewText},
	"verdict":            {shape: browserReviewText},
	"enqueued_at":        {shape: browserReviewText},
	"started_at":         {shape: browserReviewText},
	"finished_at":        {shape: browserReviewText},
	"created_at":         {shape: browserReviewText},
	"completed_at":       {shape: browserReviewText},
	"panel_name":         {shape: browserReviewText},
	"panel_run_uuid":     {shape: browserReviewText},
	"panel_role":         {shape: browserReviewText},
	"panel_member_name":  {shape: browserReviewText},
	"verdict_bool":       {shape: browserReviewVerdictBool},
	"closed":             {shape: browserReviewBool},
	"addressed":          {shape: browserReviewBool},
	"skipped":            {shape: browserReviewBool},
	"panel_member_index": {shape: browserReviewIndex},
	"parent_job_id":      {shape: browserReviewID},
	"duration_ms":        {shape: browserReviewDuration},
	"member_job_ids":     {shape: browserReviewIDs},
	"output":             {shape: browserReviewOutput},
	"text":               {shape: browserReviewOutput},
	"markdown":           {shape: browserReviewOutput},
	"result":             {shape: browserReviewOutput},
	"review":             {shape: browserReviewPayload},
}

var browserReceiptFields = map[string]browserReviewField{
	"id":             {shape: browserReviewID},
	"job_id":         {shape: browserReviewID},
	"uuid":           {shape: browserReviewText},
	"status":         {shape: browserReviewText},
	"skipped":        {shape: browserReviewBool},
	"reason":         {shape: browserReviewText},
	"git_ref":        {shape: browserReviewText},
	"branch":         {shape: browserReviewText},
	"job_type":       {shape: browserReviewText},
	"panel_name":     {shape: browserReviewText},
	"panel_run_uuid": {shape: browserReviewText},
	"panel_role":     {shape: browserReviewText},
	"member_job_ids": {shape: browserReviewIDs},
}

var browserReviewInteger = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
var browserReviewPositiveInteger = regexp.MustCompile(`^[1-9][0-9]*$`)

const (
	browserReviewMaxTextUnits   = 1024
	browserReviewMaxOutputUnits = 4 << 20
	browserReviewMaxMemberIDs   = 256
	browserReviewMaxSafeInteger = uint64(9007199254740991) // JavaScript Number.MAX_SAFE_INTEGER
)

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
		clean, err := validateBrowserValue("has_more", value, browserReviewBool)
		if err != nil {
			return nil, errors.New("review list pagination is malformed")
		}
		projected["has_more"] = clean
	}
	encoded, err := json.Marshal(projected)
	if err != nil {
		return nil, errors.New("review list response could not be projected")
	}
	return encoded, nil
}

func projectReviewArray(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, errors.New("review list collection is malformed")
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, errors.New("review list collection is malformed")
	}
	clean := make([]json.RawMessage, 0, len(rows))
	for _, row := range rows {
		projected, err := projectBrowserObject(row, browserReviewFields, browserObjectJob, true)
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
	return projectBrowserObject(raw, browserReviewDetailFields, browserObjectDetail, true)
}

func projectReviewReceiptForBrowser(raw json.RawMessage) (json.RawMessage, error) {
	return projectBrowserObject(raw, browserReceiptFields, browserObjectReceipt, false)
}

type browserObjectKind uint8

const (
	browserObjectJob browserObjectKind = iota + 1
	browserObjectDetail
	browserObjectReceipt
)

func projectBrowserObject(raw json.RawMessage, fields map[string]browserReviewField, kind browserObjectKind, requireID bool) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, errors.New("review response is malformed")
	}
	clean := make(map[string]json.RawMessage, len(object))
	hasID := false
	var idValue, jobIDValue string
	for key, value := range object {
		if key == "job" {
			if kind != browserObjectDetail {
				// A list/receipt never exposes a full nested job. Unknown values
				// are omitted, but a recognized nested object is not copied.
				continue
			}
			nested, err := projectBrowserObject(value, browserReviewFields, browserObjectJob, true)
			if err != nil {
				return nil, err
			}
			clean[key] = nested
			continue
		}
		field, ok := fields[key]
		if !ok {
			// Unknown fields are intentionally dropped, including their nested
			// content. This is the privacy boundary for future daemon fields.
			continue
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, fmt.Errorf("review field %s is null", key)
		}
		if key == "review" {
			nested, err := projectReviewPayload(value)
			if err != nil {
				return nil, err
			}
			clean[key] = nested
			continue
		}
		validated, err := validateBrowserValue(key, value, field.shape)
		if err != nil {
			return nil, err
		}
		clean[key] = validated
		if key == "id" {
			hasID = true
			idValue = browserIntegerValue(validated)
		}
		if key == "job_id" {
			hasID = true
			jobIDValue = browserIntegerValue(validated)
		}
	}
	if requireID && !hasID {
		return nil, errors.New("review response has no positive identity")
	}
	if kind == browserObjectJob && idValue != "" && jobIDValue != "" && idValue != jobIDValue {
		return nil, errors.New("review job identity is inconsistent")
	}
	encoded, err := json.Marshal(clean)
	if err != nil {
		return nil, errors.New("review response could not be projected")
	}
	return encoded, nil
}

func projectReviewPayload(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, errors.New("review payload is malformed")
	}
	if trimmed[0] == '"' {
		return validateBrowserValue("review", trimmed, browserReviewOutput)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil || object == nil {
		return nil, errors.New("review payload is malformed")
	}
	clean := make(map[string]json.RawMessage, len(object))
	for key, value := range object {
		var shape browserReviewShape
		switch key {
		case "output", "verdict":
			shape = browserReviewOutput
			if key == "verdict" {
				shape = browserReviewText
			}
		default:
			continue
		}
		validated, err := validateBrowserValue(key, value, shape)
		if err != nil {
			return nil, err
		}
		clean[key] = validated
	}
	encoded, err := json.Marshal(clean)
	if err != nil {
		return nil, errors.New("review payload could not be projected")
	}
	return encoded, nil
}

func validateBrowserValue(key string, raw json.RawMessage, shape browserReviewShape) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, fmt.Errorf("review field %s is null", key)
	}
	switch shape {
	case browserReviewText:
		var value string
		if err := json.Unmarshal(trimmed, &value); err != nil || !validBrowserReviewText(value, browserReviewMaxTextUnits) {
			return nil, fmt.Errorf("review field %s is not a bounded string", key)
		}
	case browserReviewOutput:
		var value string
		if err := json.Unmarshal(trimmed, &value); err != nil || !validBrowserReviewText(value, browserReviewMaxOutputUnits) {
			return nil, fmt.Errorf("review field %s is not a bounded output string", key)
		}
	case browserReviewBool:
		var value bool
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return nil, fmt.Errorf("review field %s is not a boolean", key)
		}
	case browserReviewVerdictBool:
		switch string(trimmed) {
		case "0", "1":
			if string(trimmed) == "0" {
				return json.RawMessage("false"), nil
			}
			return json.RawMessage("true"), nil
		default:
			return nil, fmt.Errorf("review field %s is not a v0.64 verdict integer", key)
		}
	case browserReviewID:
		if !validBrowserInteger(trimmed, true) {
			return nil, fmt.Errorf("review field %s is not a positive integer", key)
		}
	case browserReviewIndex:
		if !validBrowserInteger(trimmed, false) {
			return nil, fmt.Errorf("review field %s is not a nonnegative integer", key)
		}
	case browserReviewDuration:
		if !validBrowserJSONNumber(trimmed, false) {
			return nil, fmt.Errorf("review field %s is not a nonnegative integer", key)
		}
	case browserReviewIDs:
		var values []json.RawMessage
		if err := json.Unmarshal(trimmed, &values); err != nil || len(values) > browserReviewMaxMemberIDs {
			return nil, fmt.Errorf("review field %s is not a bounded integer array", key)
		}
		for _, value := range values {
			if !validBrowserInteger(value, true) {
				return nil, fmt.Errorf("review field %s contains an invalid integer", key)
			}
		}
	default:
		return nil, fmt.Errorf("review field %s has an unsupported shape", key)
	}
	return append(json.RawMessage(nil), trimmed...), nil
}

func validBrowserInteger(raw json.RawMessage, positive bool) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return false
	}
	if trimmed[0] == '"' {
		var value string
		if json.Unmarshal(trimmed, &value) != nil {
			return false
		}
		if (positive && !browserReviewPositiveInteger.MatchString(value)) || (!positive && !browserReviewInteger.MatchString(value)) {
			return false
		}
		parsed, err := strconv.ParseUint(value, 10, 64)
		return err == nil && parsed <= browserReviewMaxSafeInteger && (!positive || parsed > 0)
	}
	if positive && !browserReviewPositiveInteger.Match(trimmed) || !positive && !browserReviewInteger.Match(trimmed) {
		return false
	}
	parsed, err := strconv.ParseUint(string(trimmed), 10, 64)
	return err == nil && parsed <= browserReviewMaxSafeInteger && (!positive || parsed > 0)
}

func validBrowserJSONNumber(raw json.RawMessage, positive bool) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] == '"' || !browserReviewInteger.Match(trimmed) {
		return false
	}
	parsed, err := strconv.ParseUint(string(trimmed), 10, 64)
	return err == nil && parsed <= browserReviewMaxSafeInteger && (!positive || parsed > 0)
}

func browserIntegerValue(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) >= 2 && trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"' {
		var value string
		if json.Unmarshal(trimmed, &value) == nil {
			return value
		}
	}
	return string(trimmed)
}

func validBrowserReviewText(value string, maxUnits int) bool {
	if !utf8.ValidString(value) {
		return false
	}
	units := 0
	for _, char := range value {
		// Match the browser normalizer: tabs, LF, and CR are valid review
		// Markdown whitespace; other C0 controls and DEL are not.
		if char <= 0x08 || char == 0x0b || char == 0x0c || char >= 0x0e && char <= 0x1f || char == 0x7f {
			return false
		}
		if char > 0xffff {
			units += 2
		} else {
			units++
		}
		if units > maxUnits {
			return false
		}
	}
	return true
}
