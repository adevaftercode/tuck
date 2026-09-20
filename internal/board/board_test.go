package board

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"tuck/internal/task"
)

func TestProjectionLimitsAndCompletionRecency(t *testing.T) {
	b := &Board{}
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	for index := 1; index <= 12; index++ {
		for _, state := range []task.State{task.Doing, task.Todo, task.Backlog} {
			b.Tasks = append(b.Tasks, &task.Task{
				Number: index, Order: index, State: state,
				Title: fmt.Sprintf("%s-%02d", state, index),
			})
		}
		b.Tasks = append(b.Tasks, &task.Task{
			Number: index, State: task.Done, Title: fmt.Sprintf("done-%02d", index),
			CompletedAt: base.Add(time.Duration(index) * time.Hour).Format(time.RFC3339Nano),
		})
	}

	projection := string(b.Projection())
	if got := strings.Count(projection, "doing-"); got != 12 {
		t.Fatalf("projection includes %d Doing tasks, want 12", got)
	}
	if got := strings.Count(projection, "todo-"); got != 10 {
		t.Fatalf("projection includes %d Todo tasks, want 10", got)
	}
	if got := strings.Count(projection, "backlog-"); got != 10 {
		t.Fatalf("projection includes %d Backlog tasks, want 10", got)
	}
	if got := strings.Count(projection, "done-"); got != 10 {
		t.Fatalf("projection includes %d completed tasks, want 10", got)
	}
	if !strings.Contains(projection, "done-12") || strings.Contains(projection, "done-02") {
		t.Fatalf("projection did not keep the ten most recently completed tasks:\n%s", projection)
	}
	if strings.Index(projection, "done-12") > strings.Index(projection, "done-11") {
		t.Fatalf("recently completed tasks are not newest first:\n%s", projection)
	}
}

func TestLoadReportsDuplicateNumbersAndNoncanonicalFilenames(t *testing.T) {
	root := t.TempDir()
	for _, state := range task.States {
		if err := os.MkdirAll(filepath.Join(root, "tasks", string(state)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	created := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	first := task.New("wft_00000000000000000000000000", 1, 1, "First", task.Backlog, created, nil)
	first.SetPath(task.Backlog, first.Title)
	writeTask(t, root, first.Path, first)
	second := task.New("wft_00000000000000000000000001", 1, 1, "Second", task.Todo, created, nil)
	second.SetPath(task.Todo, second.Title)
	writeTask(t, root, second.Path, second)
	third := task.New(first.ID, 2, 3, "Third", task.Todo, created, nil)
	third.SetPath(task.Todo, third.Title)
	writeTask(t, root, third.Path, third)
	ten := task.New("wft_00000000000000000000000002", 10, 1, "Ten", task.Done, created, nil)
	ten.SetPath(task.Done, ten.Title)
	data, err := ten.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tasks", "done", "0010-ten.md"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	b := Load(root)
	var duplicateID, duplicateNumber, orderIssue, filenameIssue bool
	for _, issue := range b.Issues {
		duplicateID = duplicateID || strings.Contains(issue.Message, "duplicate task id")
		duplicateNumber = duplicateNumber || strings.Contains(issue.Message, "duplicate task number")
		orderIssue = orderIssue || strings.Contains(issue.Message, "order must be contiguous")
		filenameIssue = filenameIssue || strings.Contains(issue.Message, "filename number must match")
	}
	if !duplicateID || !duplicateNumber || !orderIssue || !filenameIssue {
		t.Fatalf("Load issues = %#v; expected duplicate ID/number, order, and filename issues", b.Issues)
	}
}

func TestLoadRejectsTaskSymlink(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "tasks", "backlog")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "victim.md")
	record := task.New("wft_00000000000000000000000000", 1, 0, "Secret", task.Backlog, time.Now().UTC(), nil)
	record.SetPath(task.Backlog, record.Title)
	record.Body = "external secret body"
	data, err := record.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(external, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(stateDir, "001-secret.md")); err != nil {
		if os.IsPermission(err) || errors.Is(err, syscall.Errno(1314)) {
			t.Skipf("platform does not permit creating symlinks: %v", err)
		}
		t.Fatal(err)
	}

	b := Load(root)
	if len(b.Tasks) != 0 {
		t.Fatalf("Load read %d task(s) through a symlink", len(b.Tasks))
	}
	if len(b.Issues) == 0 || !strings.Contains(b.Issues[0].Message, "regular file") {
		t.Fatalf("Load issues = %#v; want an invalid regular-file issue", b.Issues)
	}
}

func writeTask(t *testing.T, root, rel string, record *task.Task) {
	t.Helper()
	data, err := record.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), data, 0o644); err != nil {
		t.Fatal(err)
	}
}
