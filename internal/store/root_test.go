package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestResolveUsesPrimaryGitWorktree(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("Git is not installed")
	}
	base := t.TempDir()
	main := filepath.Join(base, "main")
	linked := filepath.Join(base, "feature worktree")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(directory string, args ...string) {
		t.Helper()
		command := exec.Command(git, append([]string{"-C", directory}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	runGit(main, "init")
	runGit(main, "config", "user.name", "Weft Test")
	runGit(main, "config", "user.email", "weft@example.invalid")
	if err := os.WriteFile(filepath.Join(main, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(main, "add", "README.md")
	runGit(main, "commit", "-m", "initial")
	runGit(main, "worktree", "add", "-b", "feature", linked, "HEAD")

	resolved, err := Resolve(linked, "")
	if err != nil {
		t.Fatal(err)
	}
	mainCanonical, _ := filepath.EvalSymlinks(main)
	if resolved.Path != mainCanonical {
		t.Fatalf("resolved path = %q, want primary worktree %q", resolved.Path, mainCanonical)
	}
	if resolved.GitCommon == "" || filepath.Base(resolved.GitCommon) != ".git" {
		t.Fatalf("git common directory was not resolved: %#v", resolved)
	}
	explicit, err := Resolve(linked, linked)
	if err != nil {
		t.Fatal(err)
	}
	if explicit.Path != linked || explicit.GitCommon != resolved.GitCommon {
		t.Fatalf("explicit board selection did not keep its path and shared lock directory: %#v", explicit)
	}
}
