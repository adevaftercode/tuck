package store

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Root struct {
	Path string
}

func Resolve(cwd, explicit string) (Root, error) {
	if explicit != "" {
		path, err := filepath.Abs(explicit)
		if err != nil {
			return Root{}, err
		}
		if resolved, resolveErr := filepath.EvalSymlinks(path); resolveErr == nil {
			path = resolved
		}
		return Root{Path: filepath.Clean(path)}, nil
	}
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return Root{}, err
		}
	}
	cwd, err := filepath.Abs(cwd)
	if err != nil {
		return Root{}, err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(cwd); resolveErr == nil {
		cwd = resolved
	}

	if git, lookErr := exec.LookPath("git"); lookErr == nil {
		if output, gitErr := exec.Command(git, "-C", cwd, "rev-parse", "--show-toplevel").Output(); gitErr == nil {
			repo := strings.TrimSpace(string(output))
			if !filepath.IsAbs(repo) {
				repo = filepath.Join(cwd, repo)
			}
			repo, err = filepath.Abs(repo)
			if err != nil {
				return Root{}, err
			}
			worktrees, listErr := exec.Command(git, "-C", cwd, "worktree", "list", "--porcelain").Output()
			if listErr != nil {
				return Root{}, fmt.Errorf("resolve primary Git worktree: %w", listErr)
			}
			primary := firstWorktree(string(worktrees))
			if primary == "" {
				return Root{}, errors.New("Git did not report a primary worktree; use --root to select the board")
			}
			primary, err = filepath.Abs(primary)
			if err != nil {
				return Root{}, err
			}
			primary, err = filepath.EvalSymlinks(primary)
			if err != nil {
				return Root{}, fmt.Errorf("resolve primary worktree %q: %w", primary, err)
			}
			return Root{Path: filepath.Clean(primary)}, nil
		}
	}

	// A linked worktree cannot safely infer the shared main board without Git.
	for current := cwd; ; current = filepath.Dir(current) {
		gitPath := filepath.Join(current, ".git")
		if info, statErr := os.Lstat(gitPath); statErr == nil && !info.IsDir() {
			return Root{}, errors.New("Git is required to resolve a linked worktree; install Git or pass --root")
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	for current := cwd; ; current = filepath.Dir(current) {
		if exists(filepath.Join(current, "board.md")) || exists(filepath.Join(current, "tasks")) {
			return Root{Path: current}, nil
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	return Root{Path: cwd}, nil
}

func firstWorktree(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			return strings.TrimPrefix(line, "worktree ")
		}
	}
	return ""
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
