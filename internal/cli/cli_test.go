package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func runCLI(t *testing.T, root string, args ...string) (int, string, string) {
	t.Helper()
	args = append([]string{args[0], "--root", root}, args[1:]...)
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func requireSuccess(t *testing.T, root string, args ...string) string {
	t.Helper()
	code, stdout, stderr := runCLI(t, root, args...)
	if code != 0 {
		t.Fatalf("tuck %s exited %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), code, stdout, stderr)
	}
	return stdout
}

func TestBoardLifecycleMetadataOrderingAndJSON(t *testing.T) {
	root := t.TempDir()
	requireSuccess(t, root, "init")
	requireSuccess(t, root, "add", "Product image management", "--state", "todo")
	requireSuccess(t, root, "add", "Checkout simplification", "--state", "todo")
	requireSuccess(t, root, "add", "Move sample")
	requireSuccess(t, root, "move", "003", "todo")
	requireSuccess(t, root, "move", "003", "backlog")
	requireSuccess(t, root, "reorder", "002", "--first")

	listed := requireSuccess(t, root, "list", "todo", "--json")
	var list struct {
		Tasks []taskJSON `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(listed), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Tasks) != 2 || list.Tasks[0].Number != "002" || list.Tasks[0].Order != 1 || list.Tasks[1].Order != 2 {
		t.Fatalf("unexpected reordered list: %s", listed)
	}

	requireSuccess(t, root, "meta", "set", "002", "priority", "3")
	requireSuccess(t, root, "meta", "set", "002", "estimate", "--", "-1.5")
	requireSuccess(t, root, "meta", "set", "002", "labels", `["web", "checkout"]`)
	found := requireSuccess(t, root, "find", "checkout", "--meta", "priority=3", "--meta", "estimate=-1.5", "--meta", `labels=["web","checkout"]`, "--json")
	var foundResult struct {
		Tasks []taskJSON `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(found), &foundResult); err != nil {
		t.Fatal(err)
	}
	if len(foundResult.Tasks) != 1 || foundResult.Tasks[0].Number != "002" {
		t.Fatalf("typed metadata filter returned unexpected records: %s", found)
	}
	if output := requireSuccess(t, root, "meta", "get", "002", "priority"); !strings.Contains(output, "priority = 3") {
		t.Fatalf("metadata get returned %q", output)
	}
	requireSuccess(t, root, "meta", "unset", "002", "estimate")

	requireSuccess(t, root, "rename", "002", "Checkout flow")
	requireSuccess(t, root, "start", "002")
	if output := requireSuccess(t, root, "start", "002"); !strings.Contains(output, "already doing") {
		t.Fatalf("repeated start should be a successful no-op: %q", output)
	}
	show := requireSuccess(t, root, "show", "002")
	if !strings.Contains(show, "# Checkout flow") || !strings.Contains(show, "labels: [web, checkout]") {
		t.Fatalf("show did not preserve title and metadata:\n%s", show)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "doing", "002-checkout-flow.md")); err != nil {
		t.Fatalf("renamed task did not move to doing: %v", err)
	}
	requireSuccess(t, root, "done", "002")
	donePath := filepath.Join(root, "tasks", "done", "002-checkout-flow.md")
	completedOnce, err := os.ReadFile(donePath)
	if err != nil {
		t.Fatal(err)
	}
	if output := requireSuccess(t, root, "done", "002"); !strings.Contains(output, "already done") {
		t.Fatalf("repeated done should be a successful no-op: %q", output)
	}
	completedAgain, err := os.ReadFile(donePath)
	if err != nil || !bytes.Equal(completedOnce, completedAgain) {
		t.Fatalf("repeated done changed completion data: %q, %v", completedAgain, err)
	}
	recent := requireSuccess(t, root, "recent", "--json")
	if !strings.Contains(recent, `"number": "002"`) || !strings.Contains(recent, `"completed_at"`) {
		t.Fatalf("recent JSON missing completed task data: %s", recent)
	}
	requireSuccess(t, root, "reopen", "002")
	if output := requireSuccess(t, root, "reopen", "002"); !strings.Contains(output, "already doing") {
		t.Fatalf("repeated reopen should be a successful no-op: %q", output)
	}
	reopened := requireSuccess(t, root, "show", "002", "--json")
	if strings.Contains(reopened, `"completed_at"`) {
		t.Fatalf("reopened task retained completed_at: %s", reopened)
	}
	requireSuccess(t, root, "sync")
	if output := requireSuccess(t, root, "check"); !strings.Contains(output, "board OK") {
		t.Fatalf("check output = %q", output)
	}
}

func TestSyncWarnsAndLeavesProjectionUntouchedForMalformedTask(t *testing.T) {
	root := t.TempDir()
	requireSuccess(t, root, "init")
	requireSuccess(t, root, "add", "A valid task")
	projectionPath := filepath.Join(root, "TUCK.md")
	before, err := os.ReadFile(projectionPath)
	if err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(root, "tasks", "backlog", "001-a-valid-task.md")
	if err := os.WriteFile(taskPath, []byte("not front matter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, syncOutput, stderr := runCLI(t, root, "sync", "--json")
	if code == 0 || !strings.Contains(stderr, "warning:") || !strings.Contains(stderr, "left untouched") {
		t.Fatalf("sync result code=%d stderr=%q", code, stderr)
	}
	var syncResult struct {
		Synced bool `json:"synced"`
		Issues []struct {
			Path    string `json:"path"`
			Message string `json:"message"`
		} `json:"issues"`
	}
	if err := json.Unmarshal([]byte(syncOutput), &syncResult); err != nil {
		t.Fatalf("sync JSON is invalid: %v (%s)", err, syncOutput)
	}
	if syncResult.Synced || len(syncResult.Issues) == 0 {
		t.Fatalf("sync JSON did not report malformed records: %s", syncOutput)
	}
	after, err := os.ReadFile(projectionPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("sync changed TUCK.md despite malformed task")
	}
	code, stdout, _ := runCLI(t, root, "check")
	if code == 0 || !strings.Contains(stdout, "task must begin with YAML front matter") {
		t.Fatalf("check failed to report malformed task: code=%d stdout=%q", code, stdout)
	}
}

func TestDefaultListUsesProjectionLimits(t *testing.T) {
	root := t.TempDir()
	requireSuccess(t, root, "init")
	for i := 1; i <= 12; i++ {
		requireSuccess(t, root, "add", "Backlog item", "--state", "backlog")
	}
	output := requireSuccess(t, root, "list")
	if strings.Count(output, "Backlog item") != 10 {
		t.Fatalf("default list should show only top 10 backlog tasks; got %d", strings.Count(output, "Backlog item"))
	}
	all := requireSuccess(t, root, "list", "backlog", "--json")
	if strings.Count(all, `"state": "backlog"`) != 12 {
		t.Fatalf("state-specific list should include all tasks")
	}
}

func TestConcurrentWritersKeepEveryTask(t *testing.T) {
	if root := os.Getenv("TUCK_CONCURRENT_TEST_ROOT"); root != "" {
		var stdout, stderr bytes.Buffer
		code := Run([]string{"add", "Concurrent task", "writer", os.Getenv("TUCK_CONCURRENT_TEST_WRITER"), "--root", root}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("child writer exited %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
		}
		return
	}
	root := t.TempDir()
	requireSuccess(t, root, "init")
	const writers = 12
	commands := make([]*exec.Cmd, 0, writers)
	stdout := make([]bytes.Buffer, writers)
	stderr := make([]bytes.Buffer, writers)
	for index := 0; index < writers; index++ {
		command := exec.Command(os.Args[0], "-test.run=^TestConcurrentWritersKeepEveryTask$")
		command.Env = append(os.Environ(), "TUCK_CONCURRENT_TEST_ROOT="+root,
			"TUCK_CONCURRENT_TEST_WRITER="+strconv.Itoa(index))
		command.Stdout = &stdout[index]
		command.Stderr = &stderr[index]
		if err := command.Start(); err != nil {
			t.Fatalf("start child writer %d: %v", index, err)
		}
		commands = append(commands, command)
	}
	for index, command := range commands {
		if err := command.Wait(); err != nil {
			t.Errorf("child writer %d failed: %v\nstdout:\n%s\nstderr:\n%s", index, err, stdout[index].String(), stderr[index].String())
		}
	}
	output := requireSuccess(t, root, "list", "backlog", "--json")
	var result struct {
		Tasks []taskJSON `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Tasks) != writers {
		t.Fatalf("concurrent writers left %d tasks, want %d", len(result.Tasks), writers)
	}
}

func TestCheckReportsPendingTransactionWithoutRecoveringIt(t *testing.T) {
	root := t.TempDir()
	requireSuccess(t, root, "init")
	journal := filepath.Join(root, ".tuck-txn")
	if err := os.Mkdir(journal, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(journal, "unfinished")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := runCLI(t, root, "check")
	if code == 0 || !strings.Contains(stdout, "interrupted operation needs recovery") {
		t.Fatalf("check did not report the pending journal: code=%d stdout=%q", code, stdout)
	}
	content, err := os.ReadFile(marker)
	if err != nil || string(content) != "keep" {
		t.Fatalf("check changed the pending journal: %q, %v", content, err)
	}
}

func TestCheckReportsStaleProjectionWithoutChangingIt(t *testing.T) {
	root := t.TempDir()
	requireSuccess(t, root, "init")
	projectionPath := filepath.Join(root, "TUCK.md")
	if err := os.WriteFile(projectionPath, []byte("manual edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := runCLI(t, root, "check", "--json")
	if code == 0 || !strings.Contains(stdout, "projection is stale") {
		t.Fatalf("check did not report stale projection: code=%d stdout=%q", code, stdout)
	}
	content, err := os.ReadFile(projectionPath)
	if err != nil || string(content) != "manual edit\n" {
		t.Fatalf("check changed TUCK.md: %q, %v", content, err)
	}
}

func TestBoardMaintenanceCommandsSupportJSON(t *testing.T) {
	root := t.TempDir()
	initialized := requireSuccess(t, root, "init", "--json")
	var initResult struct {
		Initialized       bool   `json:"initialized"`
		ProjectionUpdated bool   `json:"projection_updated"`
		Root              string `json:"root"`
	}
	if err := json.Unmarshal([]byte(initialized), &initResult); err != nil {
		t.Fatal(err)
	}
	if !initResult.Initialized || !initResult.ProjectionUpdated || initResult.Root != root {
		t.Fatalf("unexpected init JSON: %s", initialized)
	}

	checked := requireSuccess(t, root, "check", "--json")
	var checkResult struct {
		OK        bool            `json:"ok"`
		TaskCount int             `json:"task_count"`
		Issues    json.RawMessage `json:"issues"`
	}
	if err := json.Unmarshal([]byte(checked), &checkResult); err != nil {
		t.Fatal(err)
	}
	if !checkResult.OK || checkResult.TaskCount != 0 || string(checkResult.Issues) != "[]" {
		t.Fatalf("unexpected check JSON: %s", checked)
	}

	synced := requireSuccess(t, root, "sync", "--json")
	var syncResult struct {
		Synced    bool `json:"synced"`
		TaskCount int  `json:"task_count"`
	}
	if err := json.Unmarshal([]byte(synced), &syncResult); err != nil {
		t.Fatal(err)
	}
	if !syncResult.Synced || syncResult.TaskCount != 0 {
		t.Fatalf("unexpected sync JSON: %s", synced)
	}
}
