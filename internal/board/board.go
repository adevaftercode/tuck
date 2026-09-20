package board

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"tuck/internal/store"
	"tuck/internal/task"
)

type Issue struct {
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

func (i Issue) Error() string {
	if i.Path == "" {
		return i.Message
	}
	return i.Path + ": " + i.Message
}

type Board struct {
	Root         string
	Tasks        []*task.Task
	Issues       []Issue
	OriginalPath map[string]string
	Dirty        map[string]bool
}

func Load(root string) *Board {
	b := &Board{Root: root, OriginalPath: make(map[string]string), Dirty: make(map[string]bool)}
	boardRoot, err := os.OpenRoot(root)
	if err != nil {
		b.Issues = append(b.Issues, Issue{Path: "tasks", Message: err.Error()})
		return b
	}
	defer boardRoot.Close()
	tasksInfo, err := boardRoot.Lstat("tasks")
	if err != nil {
		b.Issues = append(b.Issues, Issue{Path: "tasks", Message: "missing tasks directory; run tuck init"})
		return b
	} else if !tasksInfo.IsDir() || tasksInfo.Mode()&os.ModeSymlink != 0 {
		b.Issues = append(b.Issues, Issue{Path: "tasks", Message: "tasks path is not a real directory"})
		return b
	}
	tasksRoot, err := openCheckedDirectory(boardRoot, "tasks", tasksInfo)
	if err != nil {
		b.Issues = append(b.Issues, Issue{Path: "tasks", Message: err.Error()})
		return b
	}
	defer tasksRoot.Close()
	knownStates := make(map[string]bool, len(task.States))
	for _, state := range task.States {
		knownStates[string(state)] = true
	}
	rootEntries, err := readRootDirectory(tasksRoot)
	if err != nil {
		b.Issues = append(b.Issues, Issue{Path: "tasks", Message: err.Error()})
		return b
	}
	for _, entry := range rootEntries {
		switch entry.Name() {
		case ".gitignore":
			if err := validateRegularEntry(tasksRoot, entry.Name()); err != nil {
				b.Issues = append(b.Issues, Issue{Path: "tasks/.gitignore", Message: err.Error()})
			}
		case ".tuck":
			if err := validateLockDirectory(tasksRoot); err != nil {
				b.Issues = append(b.Issues, Issue{Path: "tasks/.tuck", Message: err.Error()})
			}
		default:
			if !knownStates[entry.Name()] || !entry.IsDir() {
				b.Issues = append(b.Issues, Issue{Path: filepath.ToSlash(filepath.Join("tasks", entry.Name())), Message: "unexpected entry in tasks directory"})
			}
		}
	}
	for _, state := range task.States {
		stateInfo, statErr := tasksRoot.Lstat(string(state))
		if statErr == nil && (!stateInfo.IsDir() || stateInfo.Mode()&os.ModeSymlink != 0) {
			b.Issues = append(b.Issues, Issue{Path: filepath.ToSlash(filepath.Join("tasks", string(state))), Message: "state path must be a real directory"})
			continue
		}
		if statErr != nil {
			b.Issues = append(b.Issues, Issue{Path: filepath.ToSlash(filepath.Join("tasks", string(state))), Message: "missing state directory"})
			continue
		}
		stateRoot, err := openCheckedDirectory(tasksRoot, string(state), stateInfo)
		if err != nil {
			b.Issues = append(b.Issues, Issue{Path: filepath.ToSlash(filepath.Join("tasks", string(state))), Message: err.Error()})
			continue
		}
		entries, err := readRootDirectory(stateRoot)
		if err != nil {
			_ = stateRoot.Close()
			b.Issues = append(b.Issues, Issue{Path: filepath.ToSlash(filepath.Join("tasks", string(state))), Message: err.Error()})
			continue
		}
		for _, entry := range entries {
			rel := filepath.ToSlash(filepath.Join("tasks", string(state), entry.Name()))
			if entry.IsDir() {
				b.Issues = append(b.Issues, Issue{Path: rel, Message: "nested directories are not allowed in a state directory"})
				continue
			}
			if filepath.Ext(entry.Name()) != ".md" {
				b.Issues = append(b.Issues, Issue{Path: rel, Message: "task filename must end in .md"})
				continue
			}
			info, infoErr := stateRoot.Lstat(entry.Name())
			if infoErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				b.Issues = append(b.Issues, Issue{Path: rel, Message: "task file must be a regular file"})
				continue
			}
			data, err := readCheckedRegularFile(stateRoot, entry.Name(), info)
			if err != nil {
				b.Issues = append(b.Issues, Issue{Path: rel, Message: err.Error()})
				continue
			}
			parsed, err := task.Parse(data, state, rel)
			if err != nil {
				b.Issues = append(b.Issues, Issue{Path: rel, Message: err.Error()})
				continue
			}
			if err := validateFilename(entry.Name(), parsed); err != nil {
				b.Issues = append(b.Issues, Issue{Path: rel, Message: err.Error()})
			}
			b.Tasks = append(b.Tasks, parsed)
			b.OriginalPath[parsed.ID] = parsed.Path
		}
		_ = stateRoot.Close()
	}
	b.validateUniqueAndOrder()
	return b
}

// ReadTaskFile reads a board task through pinned directory handles and rejects
// task entries that are symlinks or change between inspection and opening.
func (b *Board) ReadTaskFile(rel string) ([]byte, error) {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 3 || parts[0] != "tasks" || !knownStateName(parts[1]) || filepath.Ext(parts[2]) != ".md" || parts[2] == "." || parts[2] == ".." {
		return nil, fmt.Errorf("invalid task path %q", rel)
	}
	boardRoot, err := os.OpenRoot(b.Root)
	if err != nil {
		return nil, err
	}
	defer boardRoot.Close()
	tasksInfo, err := boardRoot.Lstat("tasks")
	if err != nil || !tasksInfo.IsDir() || tasksInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("tasks path is not a real directory")
	}
	tasksRoot, err := openCheckedDirectory(boardRoot, "tasks", tasksInfo)
	if err != nil {
		return nil, err
	}
	defer tasksRoot.Close()
	stateInfo, err := tasksRoot.Lstat(parts[1])
	if err != nil || !stateInfo.IsDir() || stateInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("task state path is not a real directory")
	}
	stateRoot, err := openCheckedDirectory(tasksRoot, parts[1], stateInfo)
	if err != nil {
		return nil, err
	}
	defer stateRoot.Close()
	info, err := stateRoot.Lstat(parts[2])
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("task file must be a regular file")
	}
	return readCheckedRegularFile(stateRoot, parts[2], info)
}

func knownStateName(name string) bool {
	for _, state := range task.States {
		if name == string(state) {
			return true
		}
	}
	return false
}

func openCheckedDirectory(parent *os.Root, name string, expected os.FileInfo) (*os.Root, error) {
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	file, err := root.Open(".")
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	actual, statErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil || closeErr != nil || !os.SameFile(expected, actual) {
		_ = root.Close()
		if statErr != nil {
			return nil, statErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		return nil, fmt.Errorf("directory changed while opening")
	}
	return root, nil
}

func readRootDirectory(root *os.Root) ([]os.DirEntry, error) {
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	return directory.ReadDir(-1)
}

func readCheckedRegularFile(root *os.Root, name string, expected os.FileInfo) ([]byte, error) {
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !actual.Mode().IsRegular() || !os.SameFile(expected, actual) {
		return nil, fmt.Errorf("task file changed while opening")
	}
	singleLink, err := store.FileHasSingleLink(file)
	if err != nil {
		return nil, err
	}
	if !singleLink {
		return nil, fmt.Errorf("regular files must not be hard links")
	}
	return io.ReadAll(file)
}

func validateRegularEntry(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must be a regular file", name)
	}
	file, err := root.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil {
		return err
	}
	if !actual.Mode().IsRegular() || !os.SameFile(info, actual) {
		return fmt.Errorf("%s changed while opening", name)
	}
	singleLink, err := store.FileHasSingleLink(file)
	if err != nil {
		return err
	}
	if !singleLink {
		return fmt.Errorf("%s must not be a hard link", name)
	}
	return nil
}

func validateLockDirectory(tasksRoot *os.Root) error {
	info, err := tasksRoot.Lstat(".tuck")
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf(".tuck must be a real directory")
	}
	lockRoot, err := openCheckedDirectory(tasksRoot, ".tuck", info)
	if err != nil {
		return err
	}
	defer lockRoot.Close()
	entries, err := readRootDirectory(lockRoot)
	if err != nil {
		return err
	}
	lockFound := false
	for _, entry := range entries {
		switch entry.Name() {
		case "lock":
			lockFound = true
			if err := validateRegularEntry(lockRoot, entry.Name()); err != nil {
				return err
			}
		case "txn":
			txnInfo, err := lockRoot.Lstat(entry.Name())
			if err != nil {
				return err
			}
			if txnInfo == nil || !txnInfo.IsDir() || txnInfo.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("txn must be a real directory")
			}
			txnRoot, err := openCheckedDirectory(lockRoot, entry.Name(), txnInfo)
			if err != nil {
				return err
			}
			_ = txnRoot.Close()
		default:
			return fmt.Errorf("unexpected entry %q in lock directory", entry.Name())
		}
	}
	if !lockFound {
		return fmt.Errorf("lock file is missing")
	}
	return nil
}

func (b *Board) validateUniqueAndOrder() {
	ids := make(map[string]string)
	numbers := make(map[int]string)
	orders := make(map[task.State][]*task.Task)
	for _, t := range b.Tasks {
		if !idPattern.MatchString(t.ID) {
			b.Issues = append(b.Issues, Issue{Path: t.Path, Message: "id must use the tuck_ ULID format"})
		}
		if previous, ok := ids[t.ID]; ok {
			b.Issues = append(b.Issues, Issue{Path: t.Path, Message: fmt.Sprintf("duplicate task id also used by %s", previous)})
		} else {
			ids[t.ID] = t.Path
		}
		if previous, ok := numbers[t.Number]; ok {
			b.Issues = append(b.Issues, Issue{Path: t.Path, Message: fmt.Sprintf("duplicate task number also used by %s", previous)})
		} else {
			numbers[t.Number] = t.Path
		}
		if t.State.Active() {
			orders[t.State] = append(orders[t.State], t)
		}
	}
	for _, state := range task.States {
		tasks := orders[state]
		sort.Slice(tasks, func(i, j int) bool {
			if tasks[i].Order == tasks[j].Order {
				return tasks[i].Number < tasks[j].Number
			}
			return tasks[i].Order < tasks[j].Order
		})
		for index, t := range tasks {
			if t.Order != index+1 {
				b.Issues = append(b.Issues, Issue{Path: t.Path, Message: fmt.Sprintf("%s order must be contiguous starting at 1", state)})
			}
		}
	}
}

var idPattern = regexp.MustCompile(`^tuck_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

func validateFilename(name string, t *task.Task) error {
	if filepath.Ext(name) != ".md" {
		return fmt.Errorf("filename must end in .md")
	}
	base := strings.TrimSuffix(name, filepath.Ext(name))
	separator := strings.IndexByte(base, '-')
	if separator < 3 {
		return fmt.Errorf("filename must use NNN-slug.md")
	}
	number, err := strconv.Atoi(base[:separator])
	if err != nil || number != t.Number || base[:separator] != task.FormatNumber(t.Number) {
		return fmt.Errorf("filename number must match front matter number %s", task.FormatNumber(t.Number))
	}
	slug := base[separator+1:]
	if slug == "" || strings.Trim(slug, "-") != slug {
		return fmt.Errorf("filename must include a readable slug")
	}
	for _, r := range slug {
		if !(r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return fmt.Errorf("filename slug contains unsupported characters")
		}
	}
	return nil
}

func (b *Board) CheckIssues() []Issue { return append([]Issue{}, b.Issues...) }

func (b *Board) ProjectionIssue() (Issue, bool, error) {
	stale, err := IsProjectionStale(b.Root, b.Projection())
	if err != nil {
		return Issue{}, false, err
	}
	if stale {
		return Issue{Path: "board.md", Message: "projection is stale; run tuck sync"}, true, nil
	}
	return Issue{}, false, nil
}

func (b *Board) EnsureValid() error {
	if len(b.Issues) == 0 {
		return nil
	}
	return fmt.Errorf("%s", b.Issues[0].Error())
}

func (b *Board) Sorted(state task.State) []*task.Task {
	var result []*task.Task
	for _, t := range b.Tasks {
		if state == "" || t.State == state {
			result = append(result, t)
		}
	}
	sortTasks(result)
	return result
}

func sortTasks(tasks []*task.Task) {
	sort.Slice(tasks, func(i, j int) bool {
		a, b := tasks[i], tasks[j]
		if a.State != b.State {
			return stateRank(a.State) < stateRank(b.State)
		}
		if a.State == task.Done {
			left, _ := time.Parse(time.RFC3339Nano, a.CompletedAt)
			right, _ := time.Parse(time.RFC3339Nano, b.CompletedAt)
			if !left.Equal(right) {
				return left.After(right)
			}
		}
		if a.State.Active() && a.Order != b.Order {
			return a.Order < b.Order
		}
		return a.Number < b.Number
	})
}

func stateRank(state task.State) int {
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

func (b *Board) Recent(limit int) []*task.Task {
	var done []*task.Task
	for _, t := range b.Tasks {
		if t.State == task.Done {
			done = append(done, t)
		}
	}
	sort.Slice(done, func(i, j int) bool {
		left, _ := time.Parse(time.RFC3339Nano, done[i].CompletedAt)
		right, _ := time.Parse(time.RFC3339Nano, done[j].CompletedAt)
		if left.Equal(right) {
			return done[i].Number > done[j].Number
		}
		return left.After(right)
	})
	if limit >= 0 && len(done) > limit {
		done = done[:limit]
	}
	return done
}

func (b *Board) Projection() []byte {
	var out strings.Builder
	out.WriteString("# Tuck\n\n")
	writeSection := func(name string, tasks []*task.Task) {
		out.WriteString("## " + name + "\n\n")
		if len(tasks) == 0 {
			out.WriteString("_None_\n\n")
			return
		}
		for _, t := range tasks {
			fmt.Fprintf(&out, "- %s %s\n", t.Handle(), oneLine(t.Title))
		}
		out.WriteByte('\n')
	}
	writeSection("Doing", b.Sorted(task.Doing))
	todo := b.Sorted(task.Todo)
	if len(todo) > 10 {
		todo = todo[:10]
	}
	writeSection("Todo", todo)
	backlog := b.Sorted(task.Backlog)
	if len(backlog) > 10 {
		backlog = backlog[:10]
	}
	writeSection("Backlog", backlog)
	writeSection("Recently done", b.Recent(10))
	return []byte(out.String())
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func IsProjectionStale(root string, projection []byte) (bool, error) {
	boardRoot, err := os.OpenRoot(root)
	if err != nil {
		return false, err
	}
	defer boardRoot.Close()
	info, err := boardRoot.Lstat("board.md")
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("board.md must be a regular file")
	}
	current, err := readCheckedRegularFile(boardRoot, "board.md", info)
	if err != nil {
		return false, err
	}
	return string(current) != string(projection), nil
}

func ProjectionExists(root string) (bool, error) {
	boardRoot, err := os.OpenRoot(root)
	if err != nil {
		return false, err
	}
	defer boardRoot.Close()
	info, err := boardRoot.Lstat("board.md")
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("board.md must be a regular file")
	}
	return true, nil
}

func Now() time.Time { return time.Now().UTC() }

func (b *Board) Changes(includeProjection bool) ([]store.Change, error) {
	changes := make(map[string]store.Change)
	for _, t := range b.Tasks {
		if !b.Dirty[t.ID] {
			continue
		}
		encoded, err := t.Marshal()
		if err != nil {
			return nil, fmt.Errorf("serialize task %s: %w", t.Handle(), err)
		}
		oldPath := b.OriginalPath[t.ID]
		if oldPath != "" && oldPath != t.Path {
			changes[oldPath] = store.Change{Path: oldPath, Delete: true}
		}
		changes[t.Path] = store.Change{Path: t.Path, Data: encoded}
	}
	if includeProjection {
		changes["board.md"] = store.Change{Path: "board.md", Data: b.Projection()}
	}
	result := make([]store.Change, 0, len(changes))
	for _, change := range changes {
		result = append(result, change)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Path == "board.md" {
			return false
		}
		if result[j].Path == "board.md" {
			return true
		}
		return result[i].Path < result[j].Path
	})
	return result, nil
}

func (b *Board) Find(handle string) (*task.Task, error) {
	if number, err := task.ParseNumber(handle); err == nil {
		for _, t := range b.Tasks {
			if t.Number == number {
				return t, nil
			}
		}
		return nil, fmt.Errorf("task %s not found", task.FormatNumber(number))
	}
	for _, t := range b.Tasks {
		if t.ID == handle {
			return t, nil
		}
	}
	return nil, fmt.Errorf("task %q not found; use its number or ID", handle)
}

func (b *Board) Add(title string, state task.State, id string, now time.Time, metadata map[string]any) (*task.Task, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, fmt.Errorf("task title cannot be empty")
	}
	if strings.ContainsAny(title, "\r\n") {
		return nil, fmt.Errorf("task title must fit on one line")
	}
	if !state.Valid() {
		return nil, fmt.Errorf("invalid state %q", state)
	}
	number, order := 1, 1
	maxInt := int(^uint(0) >> 1)
	for _, current := range b.Tasks {
		if current.Number >= number {
			if current.Number == maxInt {
				return nil, fmt.Errorf("task number space is exhausted")
			}
			number = current.Number + 1
		}
		if current.State == state && state.Active() && current.Order >= order {
			if current.Order == maxInt {
				return nil, fmt.Errorf("task order space is exhausted in %s", state)
			}
			order = current.Order + 1
		}
	}
	t := task.New(id, number, order, title, state, now, metadata)
	t.SetPath(state, title)
	b.Tasks = append(b.Tasks, t)
	b.Dirty[id] = true
	return t, nil
}

func (b *Board) Move(t *task.Task, target task.State, now time.Time) (bool, error) {
	if !target.Valid() {
		return false, fmt.Errorf("invalid state %q", target)
	}
	if t.State == target {
		return false, nil
	}
	oldState := t.State
	if oldState.Active() {
		remaining := make([]*task.Task, 0)
		for _, current := range b.Sorted(oldState) {
			if current.ID != t.ID {
				remaining = append(remaining, current)
			}
		}
		b.setOrders(oldState, remaining)
	}
	t.State = target
	if target == task.Done {
		t.Order = 0
		t.CompletedAt = now.UTC().Format(time.RFC3339Nano)
	} else {
		t.CompletedAt = ""
		t.Order = 1
		for _, current := range b.Tasks {
			if current.ID != t.ID && current.State == target && current.Order >= t.Order {
				t.Order = current.Order + 1
			}
		}
	}
	t.SetPath(target, t.Title)
	b.Dirty[t.ID] = true
	if target.Active() {
		b.setOrders(target, nil)
	}
	return true, nil
}

func (b *Board) setOrders(state task.State, ordered []*task.Task) {
	if ordered == nil {
		ordered = b.Sorted(state)
	}
	for index, current := range ordered {
		order := index + 1
		if current.Order != order {
			current.Order = order
			b.Dirty[current.ID] = true
		}
	}
}

func (b *Board) Rename(t *task.Task, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("task title cannot be empty")
	}
	if strings.ContainsAny(title, "\r\n") {
		return fmt.Errorf("task title must fit on one line")
	}
	if t.Title == title {
		return nil
	}
	t.Title = title
	t.Body = string(task.ReplaceTitle([]byte(t.Body), title))
	t.SetPath(t.State, title)
	b.Dirty[t.ID] = true
	return nil
}

func (b *Board) Reorder(t *task.Task, placement string, anchor *task.Task) error {
	if !t.State.Active() {
		return fmt.Errorf("done tasks keep completion order and cannot be reordered")
	}
	ordered := b.Sorted(t.State)
	if anchor != nil && (anchor.State != t.State || anchor.ID == t.ID) {
		return fmt.Errorf("reorder anchor must be a different task in the same state")
	}
	filtered := make([]*task.Task, 0, len(ordered))
	for _, item := range ordered {
		if item.ID != t.ID {
			filtered = append(filtered, item)
		}
	}
	insert := len(filtered)
	switch placement {
	case "first":
		insert = 0
	case "before", "after":
		if anchor == nil {
			return fmt.Errorf("--%s requires an anchor task", placement)
		}
		for i, item := range filtered {
			if item.ID == anchor.ID {
				insert = i
				if placement == "after" {
					insert++
				}
				break
			}
		}
	default:
		return fmt.Errorf("choose exactly one of --first, --before, or --after")
	}
	filtered = append(filtered, nil)
	copy(filtered[insert+1:], filtered[insert:])
	filtered[insert] = t
	b.setOrders(t.State, filtered)
	return nil
}

func (b *Board) SetMetadata(t *task.Task, key string, value any) error {
	if strings.TrimSpace(key) == "" || strings.ContainsAny(key, "\r\n") {
		return fmt.Errorf("metadata key cannot be empty or contain newlines")
	}
	if task.IsReserved(key) {
		return fmt.Errorf("metadata key %q is reserved by Tuck", key)
	}
	if _, err := task.ValueYAML(value); err != nil {
		return err
	}
	t.Metadata[key] = value
	b.Dirty[t.ID] = true
	return nil
}

func (b *Board) UnsetMetadata(t *task.Task, key string) error {
	if task.IsReserved(key) {
		return fmt.Errorf("metadata key %q is reserved by Tuck", key)
	}
	delete(t.Metadata, key)
	b.Dirty[t.ID] = true
	return nil
}
