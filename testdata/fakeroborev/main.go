package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "missing command")
		os.Exit(2)
	}
	if contains(args, "--mode-error") {
		fmt.Fprintln(os.Stderr, "deterministic fake failure")
		os.Exit(7)
	}
	switch args[0] {
	case "version":
		fmt.Print(`{"name":"roborev","version":"v0.64.0"}`)
	case "status":
		fmt.Print(`{"running":true,"queued":1,"active":1}`)
	case "list":
		fmt.Print(`[{"id":42,"git_ref":"abc123","branch":"main","status":"running","agent":"codex","panel_name":"dashboard_cross","member_job_ids":[43,44,45]},{"id":43,"parent_job_id":42,"status":"done","verdict":"finding","agent":"codex"},{"id":44,"parent_job_id":42,"status":"done","verdict":"pass","agent":"claude-code"},{"id":45,"parent_job_id":42,"status":"running","agent":"grok","model":"grok-4.6"}]`)
	case "show":
		if contains(args, "--prompt") {
			fmt.Print("한글 prompt with path E:\\\\한글 프로젝트\\\\repo")
		} else {
			fmt.Print(`{"id":42,"status":"done","verdict":"finding","review":{"output":"# Finding\nConcrete fake result"}}`)
		}
	case "log":
		fmt.Print(strings.Repeat("{\"type\":\"progress\",\"message\":\"fake log\"}\n", 4))
	case "comment", "close", "cancel", "pause", "unpause":
		fmt.Println("ok")
	case "daemon":
		fmt.Println("daemon started")
	default:
		data, _ := json.Marshal(map[string]string{"error": "unsupported fake command " + args[0]})
		fmt.Fprintln(os.Stderr, string(data))
		os.Exit(2)
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
