package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"tuck/internal/board"
	"tuck/internal/id"
	"tuck/internal/store"
	"tuck/internal/task"
)

type usageError struct{ message string }

func (e usageError) Error() string { return e.message }

type options struct {
	root   string
	json   bool
	state  string
	limit  int
	recent bool
	meta   []string
	first  bool
	before string
	after  string
	pos    []string
}

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		_, _ = io.WriteString(stdout, rootHelp)
		return 0
	}
	if args[0] == "--root" {
		if len(args) < 3 {
			fmt.Fprintln(stderr, "tuck: --root requires a path and command")
			return 2
		}
		args = append([]string{args[2], "--root", args[1]}, args[3:]...)
	} else if strings.HasPrefix(args[0], "--root=") {
		root := strings.TrimPrefix(args[0], "--root=")
		if len(args) < 2 {
			fmt.Fprintln(stderr, "tuck: --root requires a command")
			return 2
		}
		args = append([]string{args[1], "--root", root}, args[2:]...)
	}
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		_, _ = io.WriteString(stdout, rootHelp)
		return 0
	}
	command := args[0]
	if command == "--version" || command == "version" {
		fmt.Fprintln(stdout, "tuck dev")
		return 0
	}
	if _, ok := commandHelp[command]; !ok {
		fmt.Fprintf(stderr, "tuck: unknown command %q\n\n%s", command, rootHelp)
		return 2
	}
	parsed, err := parseOptions(args[1:])
	if err != nil {
		fmt.Fprintf(stderr, "tuck %s: %s\n", command, err)
		return 2
	}
	if hasHelp(args[1:]) {
		fmt.Fprintln(stdout, commandHelp[command])
		return 0
	}
	if err := validateOptions(command, parsed); err != nil {
		fmt.Fprintf(stderr, "tuck %s: %s\n\n%s", command, err, commandHelp[command])
		return 2
	}
	if err := execute(command, parsed, stdout, stderr); err != nil {
		var usage usageError
		if errors.As(err, &usage) {
			fmt.Fprintf(stderr, "tuck %s: %s\n\n%s", command, usage.Error(), commandHelp[command])
			return 2
		}
		fmt.Fprintf(stderr, "tuck %s: %s\n", command, err)
		return 1
	}
	return 0
}

func parseOptions(args []string) (options, error) {
	var result options
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--root":
			if i+1 >= len(args) {
				return result, errors.New("--root requires a path")
			}
			i++
			if args[i] == "" {
				return result, errors.New("--root requires a path")
			}
			result.root = args[i]
		case strings.HasPrefix(arg, "--root="):
			result.root = strings.TrimPrefix(arg, "--root=")
			if result.root == "" {
				return result, errors.New("--root requires a path")
			}
		case arg == "--json":
			result.json = true
		case arg == "--recent":
			result.recent = true
		case arg == "--first":
			result.first = true
		case arg == "--state" || arg == "--limit" || arg == "--meta" || arg == "--before" || arg == "--after":
			if i+1 >= len(args) {
				return result, fmt.Errorf("%s requires a value", arg)
			}
			i++
			if err := setOption(&result, arg, args[i]); err != nil {
				return result, err
			}
		case strings.HasPrefix(arg, "--state="):
			if err := setOption(&result, "--state", strings.TrimPrefix(arg, "--state=")); err != nil {
				return result, err
			}
		case strings.HasPrefix(arg, "--limit="):
			if err := setOption(&result, "--limit", strings.TrimPrefix(arg, "--limit=")); err != nil {
				return result, err
			}
		case strings.HasPrefix(arg, "--meta="):
			result.meta = append(result.meta, strings.TrimPrefix(arg, "--meta="))
		case strings.HasPrefix(arg, "--before="):
			if err := setOption(&result, "--before", strings.TrimPrefix(arg, "--before=")); err != nil {
				return result, err
			}
		case strings.HasPrefix(arg, "--after="):
			if err := setOption(&result, "--after", strings.TrimPrefix(arg, "--after=")); err != nil {
				return result, err
			}
		case arg == "--help" || arg == "-h":
			// Processed by Run so command help does not need a board lock.
		case arg == "--":
			result.pos = append(result.pos, args[i+1:]...)
			return result, nil
		case strings.HasPrefix(arg, "-"):
			return result, fmt.Errorf("unknown option %q", arg)
		default:
			result.pos = append(result.pos, arg)
		}
	}
	return result, nil
}

func setOption(options *options, name, value string) error {
	if value == "" {
		return fmt.Errorf("%s requires a value", name)
	}
	switch name {
	case "--state":
		options.state = value
	case "--limit":
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 {
			return errors.New("--limit must be a positive integer")
		}
		options.limit = limit
	case "--meta":
		options.meta = append(options.meta, value)
	case "--before":
		options.before = value
	case "--after":
		options.after = value
	}
	return nil
}

func hasHelp(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}

func validateOptions(command string, opts options) error {
	allowed := map[string]bool{}
	switch command {
	case "init", "sync", "check":
		allowed["json"] = true
	case "list":
		allowed["limit"], allowed["json"] = true, true
	case "show":
		allowed["json"] = true
	case "find":
		allowed["state"], allowed["recent"], allowed["limit"], allowed["meta"], allowed["json"] = true, true, true, true, true
	case "recent":
		allowed["limit"], allowed["json"] = true, true
	case "add":
		allowed["state"], allowed["json"] = true, true
	case "start", "move", "reopen", "done", "rename":
		allowed["json"] = true
	case "reorder":
		allowed["first"], allowed["before"], allowed["after"], allowed["json"] = true, true, true, true
	case "meta":
		allowed["json"] = true
	}
	if opts.json && !allowed["json"] {
		return errors.New("--json is not supported for this command")
	}
	if opts.state != "" && !allowed["state"] {
		return errors.New("--state is not supported for this command")
	}
	if opts.recent && !allowed["recent"] {
		return errors.New("--recent is not supported for this command")
	}
	if opts.limit > 0 && !allowed["limit"] {
		return errors.New("--limit is not supported for this command")
	}
	if len(opts.meta) > 0 && !allowed["meta"] {
		return errors.New("--meta is not supported for this command")
	}
	if opts.first && !allowed["first"] || opts.before != "" && !allowed["before"] || opts.after != "" && !allowed["after"] {
		return errors.New("reorder placement options are only supported by reorder")
	}
	if opts.state != "" && !task.State(opts.state).Valid() {
		return fmt.Errorf("invalid state %q", opts.state)
	}
	if (command == "init" || command == "sync" || command == "check") && len(opts.pos) > 0 {
		return fmt.Errorf("%s takes no positional arguments", command)
	}
	return nil
}

func execute(command string, opts options, stdout, stderr io.Writer) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	root, err := store.Resolve(cwd, opts.root)
	if err != nil {
		return err
	}
	if command == "init" {
		if err := os.MkdirAll(root.Path, 0o755); err != nil {
			return err
		}
	}
	lock, err := store.Acquire(root)
	if err != nil {
		return err
	}
	defer lock.Close()
	if command != "check" {
		if err := store.Recover(root.Path); err != nil {
			return fmt.Errorf("recover interrupted board operation: %w", err)
		}
	}
	if command == "init" {
		return initialize(root.Path, stdout, opts.json)
	}
	boardState := board.Load(root.Path)
	if command == "check" {
		pending, err := store.HasPendingRecovery(root.Path)
		if err != nil {
			return err
		}
		return checkBoard(boardState, pending, stdout, opts.json)
	}
	if command == "sync" {
		return syncBoard(boardState, stdout, stderr, opts.json)
	}
	if err := boardState.EnsureValid(); err != nil {
		return err
	}
	return runBoardCommand(command, opts, boardState, stdout, stderr)
}

func initialize(root string, stdout io.Writer, jsonOutput bool) error {
	if err := ensureDirectory(filepath.Join(root, "tasks")); err != nil {
		return err
	}
	for _, state := range task.States {
		if err := ensureDirectory(filepath.Join(root, "tasks", string(state))); err != nil {
			return err
		}
	}
	b := board.Load(root)
	if err := b.EnsureValid(); err != nil {
		return fmt.Errorf("existing board is invalid: %w", err)
	}
	stale, err := board.IsProjectionStale(root, b.Projection())
	if err != nil {
		return err
	}
	projectionUpdated := false
	if stale {
		if _, err := os.Stat(filepath.Join(root, "TUCK.md")); err == nil {
			if jsonOutput {
				return writeJSON(stdout, map[string]any{"initialized": true, "root": root, "projection_updated": false})
			}
			fmt.Fprintln(stdout, "Initialized board; existing TUCK.md was left unchanged. Run `tuck sync` to regenerate it.")
			return nil
		}
		if err := store.Commit(root, []store.Change{{Path: "TUCK.md", Data: b.Projection()}}); err != nil {
			return err
		}
		projectionUpdated = true
	}
	if jsonOutput {
		return writeJSON(stdout, map[string]any{"initialized": true, "root": root, "projection_updated": projectionUpdated})
	}
	fmt.Fprintln(stdout, "Initialized Tuck board.")
	return nil
}

func ensureDirectory(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err := os.Mkdir(path, 0o755); err != nil && !os.IsExist(err) {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must be a real directory", path)
	}
	return nil
}

func checkBoard(b *board.Board, pendingRecovery bool, stdout io.Writer, jsonOutput bool) error {
	issues := b.CheckIssues()
	if pendingRecovery {
		issues = append(issues, board.Issue{Path: ".tuck-txn", Message: "an interrupted operation needs recovery; run a Tuck command other than check"})
	}
	projectionIssue, stale, err := b.ProjectionIssue()
	if err != nil {
		return err
	}
	if stale {
		issues = append(issues, projectionIssue)
	}
	if jsonOutput {
		if err := writeJSON(stdout, map[string]any{"ok": len(issues) == 0, "task_count": len(b.Tasks), "issues": issues}); err != nil {
			return err
		}
		if len(issues) == 0 {
			return nil
		}
		return fmt.Errorf("found %d board issue(s)", len(issues))
	}
	if len(issues) == 0 {
		fmt.Fprintf(stdout, "Tuck board OK (%d tasks).\n", len(b.Tasks))
		return nil
	}
	for _, issue := range issues {
		fmt.Fprintf(stdout, "error: %s\n", issue.Error())
	}
	return fmt.Errorf("found %d board issue(s)", len(issues))
}

func syncBoard(b *board.Board, stdout, stderr io.Writer, jsonOutput bool) error {
	if len(b.Issues) > 0 {
		for _, issue := range b.Issues {
			fmt.Fprintf(stderr, "warning: %s\n", issue.Error())
		}
		if jsonOutput {
			if err := writeJSON(stdout, map[string]any{"synced": false, "issues": b.Issues}); err != nil {
				return err
			}
		}
		return errors.New("sync aborted; TUCK.md was left untouched")
	}
	if err := store.Commit(b.Root, []store.Change{{Path: "TUCK.md", Data: b.Projection()}}); err != nil {
		return err
	}
	if jsonOutput {
		return writeJSON(stdout, map[string]any{"synced": true, "task_count": len(b.Tasks)})
	}
	fmt.Fprintln(stdout, "Synchronized TUCK.md.")
	return nil
}

type taskJSON struct {
	ID          string         `json:"id"`
	Number      string         `json:"number"`
	Title       string         `json:"title"`
	State       task.State     `json:"state"`
	Path        string         `json:"path"`
	Order       int            `json:"order,omitempty"`
	CreatedAt   string         `json:"created_at"`
	CompletedAt string         `json:"completed_at,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	Body        string         `json:"body,omitempty"`
}

func taskRecord(t *task.Task, includeBody bool) taskJSON {
	result := taskJSON{ID: t.ID, Number: t.Handle(), Title: t.Title, State: t.State, Path: t.Path,
		Order: t.Order, CreatedAt: t.CreatedAt, CompletedAt: t.CompletedAt, Metadata: t.Metadata}
	if includeBody {
		result.Body = t.Body
	}
	return result
}

func writeJSON(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func runBoardCommand(command string, opts options, b *board.Board, stdout, stderr io.Writer) error {
	switch command {
	case "list":
		return commandList(opts, b, stdout)
	case "show":
		return commandShow(opts, b, stdout)
	case "find":
		return commandFind(opts, b, stdout)
	case "recent":
		return commandRecent(opts, b, stdout)
	case "add":
		return commandAdd(opts, b, stdout)
	case "start":
		return commandTransition("start", opts, b, task.Doing, stdout)
	case "done":
		return commandTransition("done", opts, b, task.Done, stdout)
	case "reopen":
		return commandReopen(opts, b, stdout)
	case "move":
		return commandMove(opts, b, stdout)
	case "rename":
		return commandRename(opts, b, stdout)
	case "reorder":
		return commandReorder(opts, b, stdout)
	case "meta":
		return commandMeta(opts, b, stdout)
	default:
		return usageError{message: "unknown command"}
	}
}

func commitBoard(b *board.Board, stdout io.Writer) error {
	changes, err := b.Changes(true)
	if err != nil {
		return err
	}
	if err := store.Commit(b.Root, changes); err != nil {
		return err
	}
	return nil
}

func commandList(opts options, b *board.Board, stdout io.Writer) error {
	var tasks []*task.Task
	if len(opts.pos) > 1 {
		return usageError{message: "list accepts at most one state"}
	}
	if len(opts.pos) == 1 {
		state := task.State(opts.pos[0])
		if !state.Valid() {
			return usageError{message: fmt.Sprintf("invalid state %q", state)}
		}
		tasks = b.Sorted(state)
	} else {
		tasks = visibleBoardTasks(b)
	}
	if opts.limit > 0 && len(tasks) > opts.limit {
		tasks = tasks[:opts.limit]
	}
	if opts.json {
		return writeJSON(stdout, map[string]any{"tasks": records(tasks, false)})
	}
	if len(opts.pos) == 1 {
		writeGroup(stdout, strings.ToUpper(string(tasksState(opts.pos[0]))), tasks, true)
		return nil
	}
	if opts.limit > 0 {
		for _, t := range tasks {
			fmt.Fprintf(stdout, "%s %-7s %s\n", t.Handle(), t.State, t.Title)
		}
		return nil
	}
	writeBoardText(stdout, b)
	return nil
}

func tasksState(value string) task.State { return task.State(value) }

func visibleBoardTasks(b *board.Board) []*task.Task {
	var visible []*task.Task
	visible = append(visible, b.Sorted(task.Doing)...)
	for _, state := range []task.State{task.Todo, task.Backlog} {
		items := b.Sorted(state)
		if len(items) > 10 {
			items = items[:10]
		}
		visible = append(visible, items...)
	}
	visible = append(visible, b.Recent(10)...)
	return visible
}

func writeBoardText(out io.Writer, b *board.Board) {
	writeGroup(out, "DOING", b.Sorted(task.Doing), true)
	writeGroup(out, "TODO", firstN(b.Sorted(task.Todo), 10), true)
	writeGroup(out, "BACKLOG", firstN(b.Sorted(task.Backlog), 10), true)
	writeGroup(out, "RECENT", b.Recent(10), true)
}

func firstN(tasks []*task.Task, n int) []*task.Task {
	if len(tasks) > n {
		return tasks[:n]
	}
	return tasks
}

func writeGroup(out io.Writer, title string, tasks []*task.Task, withState bool) {
	fmt.Fprintln(out, title)
	for _, t := range tasks {
		if withState && title != "RECENT" && title != "TODO" && title != "BACKLOG" && title != "DOING" {
			fmt.Fprintf(out, "%s %s %s\n", t.Handle(), t.State, t.Title)
		} else if title == "RECENT" {
			fmt.Fprintf(out, "%s %-10s %s\n", t.Handle(), completionDate(t.CompletedAt), t.Title)
		} else {
			fmt.Fprintf(out, "%s %s\n", t.Handle(), t.Title)
		}
	}
	fmt.Fprintln(out)
}

func records(tasks []*task.Task, includeBody bool) []taskJSON {
	out := make([]taskJSON, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, taskRecord(t, includeBody))
	}
	return out
}

func commandShow(opts options, b *board.Board, stdout io.Writer) error {
	if len(opts.pos) != 1 {
		return usageError{message: "show requires one task number or ID"}
	}
	t, err := b.Find(opts.pos[0])
	if err != nil {
		return err
	}
	if opts.json {
		return writeJSON(stdout, map[string]any{"task": taskRecord(t, true)})
	}
	data, err := os.ReadFile(filepath.Join(b.Root, filepath.FromSlash(t.Path)))
	if err != nil {
		return err
	}
	_, err = stdout.Write(data)
	return err
}

func commandFind(opts options, b *board.Board, stdout io.Writer) error {
	query := strings.ToLower(strings.TrimSpace(strings.Join(opts.pos, " ")))
	if query == "" {
		return usageError{message: "find requires a query"}
	}
	if opts.recent && opts.state != "" && opts.state != string(task.Done) {
		return usageError{message: "--recent can only be combined with --state done"}
	}
	filters, err := parseMetadataFilters(opts.meta)
	if err != nil {
		return usageError{message: err.Error()}
	}
	type match struct {
		task *task.Task
		rank int
	}
	var matches []match
	for _, t := range b.Tasks {
		if opts.state != "" && string(t.State) != opts.state {
			continue
		}
		if opts.recent && t.State != task.Done {
			continue
		}
		if !metadataMatches(t.Metadata, filters) {
			continue
		}
		rank := searchRank(t, query)
		if rank < 0 {
			continue
		}
		matches = append(matches, match{task: t, rank: rank})
	}
	sort.Slice(matches, func(i, j int) bool {
		left, right := matches[i], matches[j]
		if opts.recent {
			leftAt, _ := time.Parse(time.RFC3339Nano, left.task.CompletedAt)
			rightAt, _ := time.Parse(time.RFC3339Nano, right.task.CompletedAt)
			if !leftAt.Equal(rightAt) {
				return leftAt.After(rightAt)
			}
		}
		if left.rank != right.rank {
			return left.rank < right.rank
		}
		if left.task.State != right.task.State {
			return statePriority(left.task.State) < statePriority(right.task.State)
		}
		if left.task.State.Active() && left.task.Order != right.task.Order {
			return left.task.Order < right.task.Order
		}
		return left.task.Number < right.task.Number
	})
	limit := opts.limit
	if limit == 0 {
		limit = 20
	}
	if len(matches) > limit {
		matches = matches[:limit]
	}
	results := make([]*task.Task, 0, len(matches))
	for _, item := range matches {
		results = append(results, item.task)
	}
	if opts.json {
		return writeJSON(stdout, map[string]any{"tasks": records(results, false)})
	}
	for _, t := range results {
		fmt.Fprintf(stdout, "%s %-7s %s\n", t.Handle(), t.State, t.Title)
	}
	return nil
}

func searchRank(t *task.Task, query string) int {
	if strings.Contains(strings.ToLower(t.Title), query) {
		return 0
	}
	if strings.Contains(strings.ToLower(t.Body), query) {
		return 1
	}
	encoded, _ := json.Marshal(t.Metadata)
	if strings.Contains(strings.ToLower(string(encoded)), query) {
		return 2
	}
	return -1
}

type metadataFilter struct {
	key   string
	value any
}

func parseMetadataFilters(rawFilters []string) ([]metadataFilter, error) {
	filters := make([]metadataFilter, 0, len(rawFilters))
	for _, raw := range rawFilters {
		separator := strings.IndexByte(raw, '=')
		if separator < 1 || separator == len(raw)-1 {
			return nil, fmt.Errorf("--meta must be KEY=JSON-value")
		}
		key := raw[:separator]
		if task.IsReserved(key) {
			return nil, fmt.Errorf("--meta cannot filter reserved field %q", key)
		}
		value, err := task.DecodeJSONValue(raw[separator+1:])
		if err != nil {
			return nil, fmt.Errorf("invalid --meta JSON value: %w", err)
		}
		filters = append(filters, metadataFilter{key: key, value: value})
	}
	return filters, nil
}

func metadataMatches(metadata map[string]any, filters []metadataFilter) bool {
	for _, filter := range filters {
		value, ok := metadata[filter.key]
		if !ok || !reflect.DeepEqual(normalizeMetadataNumber(value), filter.value) {
			return false
		}
	}
	return true
}

func normalizeMetadataNumber(value any) any {
	switch n := value.(type) {
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case float32:
		return float64(n)
	case []any:
		result := make([]any, len(n))
		for i, item := range n {
			result[i] = normalizeMetadataNumber(item)
		}
		return result
	default:
		return value
	}
}

func statePriority(state task.State) int {
	switch state {
	case task.Doing:
		return 0
	case task.Todo:
		return 1
	case task.Backlog:
		return 2
	default:
		return 3
	}
}

func commandRecent(opts options, b *board.Board, stdout io.Writer) error {
	if len(opts.pos) != 0 {
		return usageError{message: "recent takes no positional arguments"}
	}
	limit := opts.limit
	if limit == 0 {
		limit = 10
	}
	tasks := b.Recent(limit)
	if opts.json {
		return writeJSON(stdout, map[string]any{"tasks": records(tasks, false)})
	}
	for _, t := range tasks {
		fmt.Fprintf(stdout, "%s %-10s %s\n", t.Handle(), completionDate(t.CompletedAt), t.Title)
	}
	return nil
}

func commandAdd(opts options, b *board.Board, stdout io.Writer) error {
	if len(opts.pos) == 0 {
		return usageError{message: "add requires a title"}
	}
	state := task.Backlog
	if opts.state != "" {
		state = task.State(opts.state)
	}
	now := board.Now()
	newID, err := id.New(now)
	if err != nil {
		return err
	}
	t, err := b.Add(strings.Join(opts.pos, " "), state, newID, now, nil)
	if err != nil {
		return usageError{message: err.Error()}
	}
	if err := commitBoard(b, stdout); err != nil {
		return err
	}
	if opts.json {
		return writeJSON(stdout, map[string]any{"task": taskRecord(t, false)})
	}
	fmt.Fprintf(stdout, "Created %s %s\n%s\n", t.Handle(), t.Title, t.Path)
	return nil
}

func commandTransition(command string, opts options, b *board.Board, state task.State, stdout io.Writer) error {
	if len(opts.pos) != 1 {
		return usageError{message: command + " requires one task number or ID"}
	}
	t, err := b.Find(opts.pos[0])
	if err != nil {
		return err
	}
	changed, err := b.Move(t, state, board.Now())
	if err != nil {
		return err
	}
	if changed {
		if err := commitBoard(b, stdout); err != nil {
			return err
		}
	}
	if opts.json {
		return writeJSON(stdout, map[string]any{"task": taskRecord(t, false)})
	}
	if changed {
		fmt.Fprintf(stdout, "%s %s -> %s\n", t.Handle(), t.Title, t.State)
	} else {
		fmt.Fprintf(stdout, "%s already %s\n", t.Handle(), t.State)
	}
	return nil
}

func commandMove(opts options, b *board.Board, stdout io.Writer) error {
	if len(opts.pos) != 2 {
		return usageError{message: "move requires a task number or ID and a state"}
	}
	if !task.State(opts.pos[1]).Valid() {
		return usageError{message: fmt.Sprintf("invalid state %q", opts.pos[1])}
	}
	t, err := b.Find(opts.pos[0])
	if err != nil {
		return err
	}
	changed, err := b.Move(t, task.State(opts.pos[1]), board.Now())
	if err != nil {
		return err
	}
	if changed {
		if err := commitBoard(b, stdout); err != nil {
			return err
		}
	}
	if opts.json {
		return writeJSON(stdout, map[string]any{"task": taskRecord(t, false)})
	}
	if changed {
		fmt.Fprintf(stdout, "%s %s -> %s\n", t.Handle(), t.Title, t.State)
	} else {
		fmt.Fprintf(stdout, "%s already %s\n", t.Handle(), t.State)
	}
	return nil
}

func commandReopen(opts options, b *board.Board, stdout io.Writer) error {
	if len(opts.pos) != 1 {
		return usageError{message: "reopen requires one task number or ID"}
	}
	t, err := b.Find(opts.pos[0])
	if err != nil {
		return err
	}
	if t.State != task.Done && t.State != task.Doing {
		return fmt.Errorf("cannot reopen %s from %s; move it directly if that is intended", t.Handle(), t.State)
	}
	return commandTransitionForTask(opts, b, t, task.Doing, stdout)
}

func commandTransitionForTask(opts options, b *board.Board, t *task.Task, state task.State, stdout io.Writer) error {
	changed, err := b.Move(t, state, board.Now())
	if err != nil {
		return err
	}
	if changed {
		if err := commitBoard(b, stdout); err != nil {
			return err
		}
	}
	if opts.json {
		return writeJSON(stdout, map[string]any{"task": taskRecord(t, false)})
	}
	if changed {
		fmt.Fprintf(stdout, "%s reopened in doing\n", t.Handle())
	} else {
		fmt.Fprintf(stdout, "%s already doing\n", t.Handle())
	}
	return nil
}

func commandRename(opts options, b *board.Board, stdout io.Writer) error {
	if len(opts.pos) < 2 {
		return usageError{message: "rename requires a task number or ID and a title"}
	}
	t, err := b.Find(opts.pos[0])
	if err != nil {
		return err
	}
	if err := b.Rename(t, strings.Join(opts.pos[1:], " ")); err != nil {
		return usageError{message: err.Error()}
	}
	if b.Dirty[t.ID] {
		if err := commitBoard(b, stdout); err != nil {
			return err
		}
	}
	if opts.json {
		return writeJSON(stdout, map[string]any{"task": taskRecord(t, false)})
	}
	fmt.Fprintf(stdout, "Renamed %s %s\n", t.Handle(), t.Title)
	return nil
}

func commandReorder(opts options, b *board.Board, stdout io.Writer) error {
	if len(opts.pos) != 1 {
		return usageError{message: "reorder requires one task number or ID"}
	}
	placements := 0
	pl, anchorHandle := "", ""
	if opts.first {
		placements++
		pl = "first"
	}
	if opts.before != "" {
		placements++
		pl, anchorHandle = "before", opts.before
	}
	if opts.after != "" {
		placements++
		pl, anchorHandle = "after", opts.after
	}
	if placements != 1 {
		return usageError{message: "choose exactly one of --first, --before, or --after"}
	}
	t, err := b.Find(opts.pos[0])
	if err != nil {
		return err
	}
	var anchor *task.Task
	if anchorHandle != "" {
		anchor, err = b.Find(anchorHandle)
		if err != nil {
			return err
		}
	}
	if err := b.Reorder(t, pl, anchor); err != nil {
		return err
	}
	if err := commitBoard(b, stdout); err != nil {
		return err
	}
	if opts.json {
		return writeJSON(stdout, map[string]any{"task": taskRecord(t, false)})
	}
	fmt.Fprintf(stdout, "Reordered %s %s\n", t.Handle(), t.Title)
	return nil
}

func commandMeta(opts options, b *board.Board, stdout io.Writer) error {
	if len(opts.pos) < 2 {
		return usageError{message: "meta requires get, set, or unset and a task handle"}
	}
	action := opts.pos[0]
	t, err := b.Find(opts.pos[1])
	if err != nil {
		return err
	}
	switch action {
	case "get":
		if len(opts.pos) > 3 {
			return usageError{message: "meta get accepts an optional key"}
		}
		if opts.json {
			if len(opts.pos) == 3 {
				value, ok := t.Metadata[opts.pos[2]]
				if !ok {
					return fmt.Errorf("metadata key %q is not set", opts.pos[2])
				}
				return writeJSON(stdout, map[string]any{"task": t.Handle(), "key": opts.pos[2], "value": value})
			}
			return writeJSON(stdout, map[string]any{"task": t.Handle(), "metadata": t.Metadata})
		}
		if len(opts.pos) == 3 {
			value, ok := t.Metadata[opts.pos[2]]
			if !ok {
				return fmt.Errorf("metadata key %q is not set", opts.pos[2])
			}
			encoded, _ := json.Marshal(value)
			fmt.Fprintf(stdout, "%s = %s\n", opts.pos[2], encoded)
			return nil
		}
		keys := make([]string, 0, len(t.Metadata))
		for key := range t.Metadata {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			encoded, _ := json.Marshal(t.Metadata[key])
			fmt.Fprintf(stdout, "%s = %s\n", key, encoded)
		}
		return nil
	case "set":
		if len(opts.pos) != 4 {
			return usageError{message: "meta set requires a task, key, and one JSON value"}
		}
		value, err := task.DecodeJSONValue(opts.pos[3])
		if err != nil {
			return usageError{message: "metadata value must be one JSON scalar or flat array: " + err.Error()}
		}
		if err := b.SetMetadata(t, opts.pos[2], value); err != nil {
			return usageError{message: err.Error()}
		}
		if err := commitBoard(b, stdout); err != nil {
			return err
		}
		if opts.json {
			return writeJSON(stdout, map[string]any{"task": taskRecord(t, false)})
		}
		fmt.Fprintf(stdout, "Set %s on %s\n", opts.pos[2], t.Handle())
		return nil
	case "unset":
		if len(opts.pos) != 3 {
			return usageError{message: "meta unset requires a task and key"}
		}
		if task.IsReserved(opts.pos[2]) {
			return usageError{message: fmt.Sprintf("metadata key %q is reserved by Tuck", opts.pos[2])}
		}
		if _, ok := t.Metadata[opts.pos[2]]; !ok {
			if opts.json {
				return writeJSON(stdout, map[string]any{"task": taskRecord(t, false)})
			}
			fmt.Fprintf(stdout, "%s is not set on %s\n", opts.pos[2], t.Handle())
			return nil
		}
		if err := b.UnsetMetadata(t, opts.pos[2]); err != nil {
			return usageError{message: err.Error()}
		}
		if err := commitBoard(b, stdout); err != nil {
			return err
		}
		if opts.json {
			return writeJSON(stdout, map[string]any{"task": taskRecord(t, false)})
		}
		fmt.Fprintf(stdout, "Unset %s on %s\n", opts.pos[2], t.Handle())
		return nil
	default:
		return usageError{message: fmt.Sprintf("unknown metadata operation %q", action)}
	}
}

func completionDate(timestamp string) string {
	completed, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return "?"
	}
	return completed.UTC().Format("2006-01-02")
}
