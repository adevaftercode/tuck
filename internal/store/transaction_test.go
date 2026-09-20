package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestRecoverRollsForwardAnInterruptedTransaction(t *testing.T) {
	root := t.TempDir()
	firstPath := "tasks/todo/001-first.md"
	secondPath := "board.md"
	firstOld, firstNew := []byte("old task\n"), []byte("new task\n")
	secondOld, secondNew := []byte("old board\n"), []byte("new board\n")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, firstPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, firstPath), firstNew, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, secondPath), secondOld, 0o644); err != nil {
		t.Fatal(err)
	}
	staleTemp := filepath.Join(root, ".tuck-write-stale")
	if err := os.WriteFile(staleTemp, []byte("partial temp"), 0o600); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(root, transactionDirectory)
	images := filepath.Join(journal, "images")
	if err := os.MkdirAll(images, 0o700); err != nil {
		t.Fatal(err)
	}
	entries := []transactionChange{
		{Path: firstPath, BeforeExists: true, BeforeHash: hash(firstOld), AfterExists: true, AfterHash: hash(firstNew), BeforeFile: "before-000000", AfterFile: "after-000000"},
		{Path: secondPath, BeforeExists: true, BeforeHash: hash(secondOld), AfterExists: true, AfterHash: hash(secondNew), BeforeFile: "before-000001", AfterFile: "after-000001"},
	}
	for i, pair := range [][2][]byte{{firstOld, firstNew}, {secondOld, secondNew}} {
		if err := os.WriteFile(filepath.Join(images, entries[i].BeforeFile), pair[0], 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(images, entries[i].AfterFile), pair[1], 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := json.Marshal(transactionManifest{Version: 1, Changes: entries})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(journal, "manifest.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Recover(root); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string][]byte{firstPath: firstNew, secondPath: secondNew} {
		got, err := os.ReadFile(filepath.Join(root, path))
		if err != nil || string(got) != string(want) {
			t.Fatalf("recovered %s = %q, %v; want %q", path, got, err, want)
		}
	}
	if _, err := os.Stat(journal); !os.IsNotExist(err) {
		t.Fatalf("journal should be removed after recovery, stat error: %v", err)
	}
	if _, err := os.Stat(staleTemp); !os.IsNotExist(err) {
		t.Fatalf("stale atomic-write temporary file should be removed, stat error: %v", err)
	}
}

func TestRecoverDoesNotOverwriteAnExternalConflict(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "board.md")
	if err := os.WriteFile(target, []byte("external edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(root, transactionDirectory)
	images := filepath.Join(journal, "images")
	if err := os.MkdirAll(images, 0o700); err != nil {
		t.Fatal(err)
	}
	before, after := []byte("old"), []byte("new")
	if err := os.WriteFile(filepath.Join(images, "before-000000"), before, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(images, "after-000000"), after, 0o600); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(transactionManifest{Version: 1, Changes: []transactionChange{{
		Path: "board.md", BeforeExists: true, BeforeHash: hash(before), AfterExists: true,
		AfterHash: hash(after), BeforeFile: "before-000000", AfterFile: "after-000000",
	}}})
	if err := os.WriteFile(filepath.Join(journal, "manifest.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Recover(root); err == nil {
		t.Fatal("expected a recovery conflict")
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "external edit" {
		t.Fatalf("external edit was changed: %q, %v", got, err)
	}
}

func TestCommitPreparationFailureLeavesBoardFilesUntouched(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "tasks", "todo")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(stateDir, "001-one.md")
	if err := os.WriteFile(taskPath, []byte("before task"), 0o644); err != nil {
		t.Fatal(err)
	}
	// board.md being a directory makes staging fail after the first change was
	// staged. Since target replacement starts only after the journal is ready,
	// the task file must still contain its original bytes.
	if err := os.Mkdir(filepath.Join(root, "board.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := Commit(root, []Change{
		{Path: "tasks/todo/001-one.md", Data: []byte("after task")},
		{Path: "board.md", Data: []byte("projection")},
	})
	if err == nil {
		t.Fatal("expected staging to fail when board.md is a directory")
	}
	got, readErr := os.ReadFile(taskPath)
	if readErr != nil || string(got) != "before task" {
		t.Fatalf("staged failure changed task file: %q, %v", got, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(root, transactionDirectory)); !os.IsNotExist(statErr) {
		t.Fatalf("failed staging should remove its journal, stat error: %v", statErr)
	}
}

func TestCommitDoesNotMutateChanges(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tasks", "todo"), 0o755); err != nil {
		t.Fatal(err)
	}
	changes := []Change{{Path: `tasks\todo\001-one.md`, Data: []byte("task")}}
	originalPath := changes[0].Path
	if err := Commit(root, changes); err != nil {
		t.Fatal(err)
	}
	if changes[0].Path != originalPath {
		t.Fatalf("Commit changed caller path to %q; want %q", changes[0].Path, originalPath)
	}
}

func TestSafeRelativeRejectsPathsOutsideBoard(t *testing.T) {
	for _, candidate := range []string{"../outside", "tasks/../board.md", "README.md", "tasks/todo/subdir/001-a.md", `tasks\\todo\\001-a.md`} {
		if _, err := safeRelative(candidate); err == nil {
			t.Errorf("safeRelative(%q) unexpectedly succeeded", candidate)
		}
	}
	for _, candidate := range []string{"board.md", "tasks/backlog/001-a.md", `tasks\todo\001-a.md`} {
		if _, err := safeRelative(candidate); err != nil {
			t.Errorf("safeRelative(%q): %v", candidate, err)
		}
	}
}

func TestCommitRejectsSymlinkedTaskParentOrTarget(t *testing.T) {
	for _, symlinkTarget := range []string{"parent", "task"} {
		t.Run(symlinkTarget, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			outsideTask := filepath.Join(outside, "001-one.md")
			if err := os.WriteFile(outsideTask, []byte("outside sentinel"), 0o644); err != nil {
				t.Fatal(err)
			}
			statePath := filepath.Join(root, "tasks", "todo")
			if symlinkTarget == "parent" {
				if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, statePath); err != nil {
					skipIfSymlinkUnavailable(t, err)
				}
			} else {
				if err := os.MkdirAll(statePath, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outsideTask, filepath.Join(statePath, "001-one.md")); err != nil {
					skipIfSymlinkUnavailable(t, err)
				}
			}
			if err := Commit(root, []Change{{Path: "tasks/todo/001-one.md", Data: []byte("replacement")}}); err == nil {
				t.Fatal("Commit succeeded through a symlink")
			}
			assertFileContents(t, outsideTask, "outside sentinel")
		})
	}
}

func TestRecoverRejectsSymlinkedTaskParentOrTarget(t *testing.T) {
	for _, symlinkTarget := range []string{"parent", "task"} {
		t.Run(symlinkTarget, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			outsideTask := filepath.Join(outside, "001-one.md")
			if err := os.WriteFile(outsideTask, []byte("old sentinel"), 0o644); err != nil {
				t.Fatal(err)
			}
			statePath := filepath.Join(root, "tasks", "todo")
			if symlinkTarget == "parent" {
				if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, statePath); err != nil {
					skipIfSymlinkUnavailable(t, err)
				}
			} else {
				if err := os.MkdirAll(statePath, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outsideTask, filepath.Join(statePath, "001-one.md")); err != nil {
					skipIfSymlinkUnavailable(t, err)
				}
			}
			writeInterruptedTaskTransaction(t, root, "tasks/todo/001-one.md", []byte("old sentinel"), []byte("new content"))
			if err := Recover(root); err == nil {
				t.Fatal("Recover succeeded through a symlink")
			}
			assertFileContents(t, outsideTask, "old sentinel")
		})
	}
}

func writeInterruptedTaskTransaction(t *testing.T, root, target string, before, after []byte) {
	t.Helper()
	journal := filepath.Join(root, transactionDirectory)
	images := filepath.Join(journal, "images")
	if err := os.MkdirAll(images, 0o700); err != nil {
		t.Fatal(err)
	}
	change := transactionChange{
		Path: target, BeforeExists: true, BeforeHash: hash(before), BeforeFile: "before-000000",
		AfterExists: true, AfterHash: hash(after), AfterFile: "after-000000",
	}
	for name, data := range map[string][]byte{change.BeforeFile: before, change.AfterFile: after} {
		if err := os.WriteFile(filepath.Join(images, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	encoded, err := json.Marshal(transactionManifest{Version: 1, Changes: []transactionChange{change}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(journal, "manifest.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertFileContents(t *testing.T, path, expected string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != expected {
		t.Fatalf("file %s = %q, %v; want %q", path, data, err, expected)
	}
}

func skipIfSymlinkUnavailable(t *testing.T, err error) {
	t.Helper()
	if os.IsPermission(err) || errors.Is(err, syscall.Errno(1314)) {
		t.Skipf("platform does not permit creating symlinks: %v", err)
	}
	t.Fatal(err)
}

func TestBoardLockSerializesProcesses(t *testing.T) {
	root := Root{Path: t.TempDir()}
	first, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	secondAcquired := make(chan *Lock, 1)
	secondError := make(chan error, 1)
	go func() {
		lock, err := Acquire(root)
		if err != nil {
			secondError <- err
			return
		}
		secondAcquired <- lock
	}()
	select {
	case lock := <-secondAcquired:
		_ = lock.Close()
		t.Fatal("second process acquired the board lock while the first held it")
	case err := <-secondError:
		t.Fatal(err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case lock := <-secondAcquired:
		if err := lock.Close(); err != nil {
			t.Fatal(err)
		}
	case err := <-secondError:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("second process did not acquire the released lock")
	}
}
