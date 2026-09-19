package board

import (
	"fmt"
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
	stateRoot := filepath.Join(root, "tasks")
	if info, err := os.Lstat(stateRoot); err != nil {
		b.Issues = append(b.Issues, Issue{Path: "tasks", Message: "missing tasks directory; run tuck init"})
		return b
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		b.Issues = append(b.Issues, Issue{Path: "tasks", Message: "tasks path is not a real directory"})
		return b
	}
	knownStates := make(map[string]bool, len(task.States))
	for _, state := range task.States {
		knownStates[string(state)] = true
	}
	rootEntries, err := os.ReadDir(stateRoot)
	if err != nil {
		b.Issues = append(b.Issues, Issue{Path: "tasks", Message: err.Error()})
		return b
	}
	for _, entry := range rootEntries {
		if !knownStates[entry.Name()] || !entry.IsDir() {
			b.Issues = append(b.Issues, Issue{Path: filepath.ToSlash(filepath.Join("tasks", entry.Name())), Message: "unexpected entry in tasks directory"})
		}
	}
	for _, state := range task.States {
		directory := filepath.Join(stateRoot, string(state))
		stateInfo, statErr := os.Lstat(directory)
		if statErr == nil && (!stateInfo.IsDir() || stateInfo.Mode()&os.ModeSymlink != 0) {
			b.Issues = append(b.Issues, Issue{Path: filepath.ToSlash(filepath.Join("tasks", string(state))), Message: "state path must be a real directory"})
			continue
		}
		entries, err := os.ReadDir(directory)
		if err != nil {
			b.Issues = append(b.Issues, Issue{Path: filepath.ToSlash(filepath.Join("tasks", string(state))), Message: "missing state directory"})
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
			path := filepath.Join(directory, entry.Name())
			info, infoErr := entry.Info()
			if infoErr != nil || !info.Mode().IsRegular() {
				b.Issues = append(b.Issues, Issue{Path: rel, Message: "task file must be a regular file"})
				continue
			}
			data, err := os.ReadFile(path)
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
	}
	b.validateUniqueAndOrder()
	return b
}

func (b *Board) validateUniqueAndOrder() {
	ids := make(map[string]string)
	numbers := make(map[int]string)
	orders := make(map[task.State][]*task.Task)
	for _, t := range b.Tasks {
		if !idPattern.MatchString(t.ID) {
			b.Issues = append(b.Issues, Issue{Path: t.Path, Message: "id must use the wft_ ULID format"})
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

var idPattern = regexp.MustCompile(`^wft_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

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
	current, err := os.ReadFile(filepath.Join(root, "board.md"))
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	return string(current) != string(projection), nil
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
