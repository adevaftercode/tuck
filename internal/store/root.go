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
	Path      string
	GitCommon string
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
		return Root{Path: filepath.Clean(path), GitCommon: gitCommonFor(path)}, nil
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
			commonOutput, commonErr := exec.Command(git, "-C", cwd, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
			if commonErr != nil {
				return Root{}, fmt.Errorf("resolve Git common directory: %w", commonErr)
			}
			common := strings.TrimSpace(string(commonOutput))
			if !filepath.IsAbs(common) {
				common = filepath.Join(repo, common)
			}
			common, err = filepath.Abs(common)
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
			common, err = filepath.EvalSymlinks(common)
			if err != nil {
				return Root{}, fmt.Errorf("resolve Git common directory: %w", err)
			}
			return Root{Path: filepath.Clean(primary), GitCommon: filepath.Clean(common)}, nil
		}
	}

	// A linked worktree cannot safely infer the shared main board without Git.
	for current := cwd; ; current = filepath.Dir(current) {
		gitPath := filepath.Join(current, ".git")
		if info, statErr := os.Stat(gitPath); statErr == nil && !info.IsDir() {
			return Root{}, errors.New("Git is required to resolve a linked worktree; install Git or pass --root")
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	for current := cwd; ; current = filepath.Dir(current) {
		if exists(filepath.Join(current, "TUCK.md")) || exists(filepath.Join(current, "tasks")) {
			return Root{Path: current}, nil
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	return Root{Path: cwd}, nil
}

func gitCommonFor(path string) string {
	git, err := exec.LookPath("git")
	if err != nil {
		return ""
	}
	probe := path
	for {
		if _, statErr := os.Stat(probe); statErr == nil {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return ""
		}
		probe = parent
	}
	output, err := exec.Command(git, "-C", probe, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return ""
	}
	common := strings.TrimSpace(string(output))
	if !filepath.IsAbs(common) {
		common = filepath.Join(probe, common)
	}
	common, err = filepath.Abs(common)
	if err != nil {
		return ""
	}
	if resolved, resolveErr := filepath.EvalSymlinks(common); resolveErr == nil {
		common = resolved
	}
	return filepath.Clean(common)
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
	_, err := os.Stat(path)
	return err == nil
}
