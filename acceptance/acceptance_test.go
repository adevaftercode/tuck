package acceptance

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

func TestAcceptance(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	binaryName := "tuck"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	binaryPath := filepath.Join(binDir, binaryName)
	build := exec.Command(goBinary, "build", "-o", binaryPath, "./cmd/tuck")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build tuck: %v\n%s", err, output)
	}

	testscript.Run(t, testscript.Params{
		Dir:                 "testdata",
		RequireExplicitExec: true,
		RequireUniqueNames:  true,
		Cmds: map[string]func(*testscript.TestScript, bool, []string){
			"json":            checkJSON,
			"capture-task-id": captureTaskID,
			"exitcode":        checkExitCode,
		},
		Setup: func(env *testscript.Env) error {
			env.Values["tuckBinary"] = binaryPath
			env.Setenv("PATH", binDir+string(os.PathListSeparator)+env.Getenv("PATH"))
			home := filepath.Join(env.WorkDir, "home")
			env.Setenv("HOME", home)
			env.Setenv("USERPROFILE", home)
			env.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
			env.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
			return nil
		},
	})
}

func checkExitCode(ts *testscript.TestScript, _ bool, args []string) {
	if len(args) < 2 {
		ts.Fatalf("exitcode requires an expected status and a Tuck command")
	}
	expected, err := strconv.Atoi(args[0])
	if err != nil {
		ts.Fatalf("invalid expected exit status %q", args[0])
	}
	binary, ok := ts.Value("tuckBinary").(string)
	if !ok || binary == "" {
		ts.Fatalf("Tuck binary path was not set up")
	}
	cmd := exec.Command(binary, args[1:]...)
	cmd.Dir = ts.MkAbs(".")
	cmd.Env = scriptEnvironment(ts)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	actual := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			ts.Fatalf("run Tuck: %v", err)
		}
		actual = exitErr.ExitCode()
	}
	if actual != expected {
		ts.Fatalf("Tuck exited with status %d, want %d", actual, expected)
	}
	_, _ = fmt.Fprint(ts.Stdout(), stdout.String())
	_, _ = fmt.Fprint(ts.Stderr(), stderr.String())
}

func scriptEnvironment(ts *testscript.TestScript) []string {
	environment := make(map[string]string)
	normalize := func(key string) string {
		if runtime.GOOS == "windows" {
			return strings.ToLower(key)
		}
		return key
	}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			environment[normalize(key)] = key + "=" + value
		}
	}
	for _, key := range []string{"PATH", "HOME", "USERPROFILE", "LOCALAPPDATA", "XDG_CACHE_HOME", "TMPDIR", "TMP"} {
		if value := ts.Getenv(key); value != "" {
			environment[normalize(key)] = key + "=" + value
		}
	}
	environment[normalize("PWD")] = "PWD=" + ts.MkAbs(".")
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, environment[key])
	}
	return result
}

func checkJSON(ts *testscript.TestScript, _ bool, args []string) {
	if len(args) != 0 {
		ts.Fatalf("json takes no arguments")
	}
	var value any
	if err := json.Unmarshal([]byte(ts.ReadFile("stdout")), &value); err != nil {
		ts.Fatalf("stdout is not valid JSON: %v", err)
	}
}

func captureTaskID(ts *testscript.TestScript, _ bool, args []string) {
	if len(args) != 0 {
		ts.Fatalf("capture-task-id takes no arguments")
	}
	var output struct {
		Task struct {
			ID string `json:"id"`
		} `json:"task"`
	}
	if err := json.Unmarshal([]byte(ts.ReadFile("stdout")), &output); err != nil {
		ts.Fatalf("cannot read task ID from stdout JSON: %v", err)
	}
	if len(output.Task.ID) != 30 || !strings.HasPrefix(output.Task.ID, "wft_") {
		ts.Fatalf("stdout task ID %q does not have the Tuck ID shape", output.Task.ID)
	}
	ts.Setenv("TASK_ID", output.Task.ID)
}
