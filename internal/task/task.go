package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"
)

type State string

const (
	Backlog State = "backlog"
	Todo    State = "todo"
	Doing   State = "doing"
	Done    State = "done"
)

var States = []State{Backlog, Todo, Doing, Done}

func (s State) Valid() bool {
	for _, candidate := range States {
		if s == candidate {
			return true
		}
	}
	return false
}

func (s State) Active() bool { return s == Backlog || s == Todo || s == Doing }

const (
	FieldID          = "id"
	FieldNumber      = "number"
	FieldOrder       = "order"
	FieldCreatedAt   = "created_at"
	FieldCompletedAt = "completed_at"
)

var reserved = map[string]bool{
	FieldID: true, FieldNumber: true, FieldOrder: true,
	FieldCreatedAt: true, FieldCompletedAt: true,
}

type Task struct {
	ID          string         `json:"id"`
	Number      int            `json:"number"`
	Order       int            `json:"order,omitempty"`
	CreatedAt   string         `json:"created_at"`
	CompletedAt string         `json:"completed_at,omitempty"`
	State       State          `json:"state"`
	Title       string         `json:"title"`
	Path        string         `json:"path"`
	Body        string         `json:"body,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`

	frontMatter *yaml.Node
}

func IsReserved(key string) bool { return reserved[key] }

func Parse(data []byte, state State, relPath string) (*Task, error) {
	body, front, err := splitFrontMatter(data)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(front, &doc); err != nil {
		return nil, fmt.Errorf("malformed front matter: %w", err)
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("front matter must be a flat mapping")
	}
	mapping := doc.Content[0]
	values := make(map[string]*yaml.Node, len(mapping.Content)/2)
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		keyNode, valueNode := mapping.Content[i], mapping.Content[i+1]
		if keyNode.Kind != yaml.ScalarNode || keyNode.Tag != "!!str" {
			return nil, errors.New("front matter keys must be strings")
		}
		key := keyNode.Value
		if _, exists := values[key]; exists {
			return nil, fmt.Errorf("duplicate front matter key %q", key)
		}
		values[key] = valueNode
	}

	readString := func(key string, required bool) (string, error) {
		node := values[key]
		if node == nil {
			if required {
				return "", fmt.Errorf("missing required field %q", key)
			}
			return "", nil
		}
		if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
			return "", fmt.Errorf("field %q must be a string", key)
		}
		return node.Value, nil
	}
	readInt := func(key string, required bool) (int, error) {
		node := values[key]
		if node == nil {
			if required {
				return 0, fmt.Errorf("missing required field %q", key)
			}
			return 0, nil
		}
		if node.Kind != yaml.ScalarNode || node.Tag != "!!int" {
			return 0, fmt.Errorf("field %q must be an integer", key)
		}
		var value int64
		maxInt := int64(^uint(0) >> 1)
		minInt := -maxInt - 1
		if err := node.Decode(&value); err != nil || value < minInt || value > maxInt {
			return 0, fmt.Errorf("field %q must be a valid integer", key)
		}
		return int(value), nil
	}

	t := &Task{State: state, Path: relPath, Body: string(body), Metadata: make(map[string]any), frontMatter: &doc}
	if t.ID, err = readString(FieldID, true); err != nil {
		return nil, err
	}
	if t.Number, err = readInt(FieldNumber, true); err != nil {
		return nil, err
	}
	if t.Order, err = readInt(FieldOrder, state.Active()); err != nil {
		return nil, err
	}
	if t.CreatedAt, err = readString(FieldCreatedAt, true); err != nil {
		return nil, err
	}
	if t.CompletedAt, err = readString(FieldCompletedAt, state == Done); err != nil {
		return nil, err
	}
	if state == Done && values[FieldOrder] != nil {
		return nil, fmt.Errorf("field %q is only allowed for active tasks", FieldOrder)
	}
	if t.ID == "" || t.Number < 1 || state.Active() && t.Order < 1 {
		return nil, errors.New("id, number, and order must be non-empty positive values")
	}
	if _, err := time.Parse(time.RFC3339Nano, t.CreatedAt); err != nil {
		return nil, fmt.Errorf("field %q must be an RFC3339 timestamp", FieldCreatedAt)
	}
	if state == Done {
		if _, err := time.Parse(time.RFC3339Nano, t.CompletedAt); err != nil {
			return nil, fmt.Errorf("field %q must be an RFC3339 timestamp", FieldCompletedAt)
		}
	} else if t.CompletedAt != "" {
		return nil, fmt.Errorf("field %q is only allowed in done tasks", FieldCompletedAt)
	}

	for key, node := range values {
		if reserved[key] {
			continue
		}
		value, err := decodeUserValue(node)
		if err != nil {
			return nil, fmt.Errorf("metadata %q: %w", key, err)
		}
		t.Metadata[key] = value
	}
	t.Title = FirstHeading(body)
	if strings.TrimSpace(t.Title) == "" {
		return nil, errors.New("task needs a first-level heading")
	}
	return t, nil
}

func splitFrontMatter(data []byte) (body []byte, front []byte, err error) {
	text := string(data)
	if strings.HasPrefix(text, "\ufeff") {
		text = strings.TrimPrefix(text, "\ufeff")
	}
	lines := strings.SplitAfter(text, "\n")
	if len(lines) < 3 || strings.TrimSpace(strings.TrimSuffix(lines[0], "\r\n")) != "---" {
		return nil, nil, errors.New("task must begin with YAML front matter")
	}
	position := len(lines[0])
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(strings.TrimSuffix(lines[i], "\n"))
		line = strings.TrimSuffix(line, "\r")
		if line == "---" {
			return []byte(text[position+len(lines[i]):]), []byte(strings.Join(lines[1:i], "")), nil
		}
		position += len(lines[i])
	}
	return nil, nil, errors.New("unterminated YAML front matter")
}

func decodeUserValue(node *yaml.Node) (any, error) {
	switch node.Kind {
	case yaml.ScalarNode:
		var value any
		if err := node.Decode(&value); err != nil {
			return nil, err
		}
		switch typed := value.(type) {
		case string, bool, float64, nil:
			return value, nil
		case int:
			return int64(typed), nil
		case int64:
			return typed, nil
		case uint64:
			if typed > math.MaxInt64 {
				return nil, errors.New("integer is out of range")
			}
			return int64(typed), nil
		default:
			return nil, fmt.Errorf("unsupported scalar type %T", value)
		}
	case yaml.SequenceNode:
		values := make([]any, 0, len(node.Content))
		for _, child := range node.Content {
			if child.Kind != yaml.ScalarNode {
				return nil, errors.New("arrays may contain only scalar values")
			}
			value, err := decodeUserValue(child)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		return values, nil
	default:
		return nil, errors.New("only strings, numbers, booleans, null, and flat arrays are supported")
	}
}

func (t *Task) Marshal() ([]byte, error) {
	if t.frontMatter == nil || len(t.frontMatter.Content) != 1 || t.frontMatter.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("task has no valid front matter mapping")
	}
	mapping := t.frontMatter.Content[0]
	setScalar := func(key, tag, value string) {
		setNode(mapping, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value})
	}
	setScalar(FieldID, "!!str", t.ID)
	setScalar(FieldNumber, "!!int", strconv.Itoa(t.Number))
	if t.State.Active() {
		setScalar(FieldOrder, "!!int", strconv.Itoa(t.Order))
	} else {
		deleteNode(mapping, FieldOrder)
	}
	setScalar(FieldCreatedAt, "!!str", t.CreatedAt)
	if t.State == Done {
		setScalar(FieldCompletedAt, "!!str", t.CompletedAt)
	} else {
		deleteNode(mapping, FieldCompletedAt)
	}
	metadataKeys := make([]string, 0, len(t.Metadata))
	for key := range t.Metadata {
		metadataKeys = append(metadataKeys, key)
	}
	sort.Strings(metadataKeys)
	for _, key := range metadataKeys {
		value := t.Metadata[key]
		if reserved[key] {
			return nil, fmt.Errorf("metadata key %q is reserved", key)
		}
		node, err := valueNode(value)
		if err != nil {
			return nil, fmt.Errorf("metadata %q: %w", key, err)
		}
		setNode(mapping, key, node)
	}
	for key := range mappingKeys(mapping) {
		if !reserved[key] {
			if _, ok := t.Metadata[key]; !ok {
				deleteNode(mapping, key)
			}
		}
	}

	var encoded bytes.Buffer
	encoder := yaml.NewEncoder(&encoded)
	encoder.SetIndent(2)
	if err := encoder.Encode(mapping); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	result := []byte("---\n" + encoded.String() + "---\n")
	result = append(result, ReplaceTitle([]byte(t.Body), t.Title)...)
	return result, nil
}

func valueNode(value any) (*yaml.Node, error) {
	var node yaml.Node
	switch v := value.(type) {
	case string:
		node = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
	case bool:
		node = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(v)}
	case int:
		node = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(v)}
	case int64:
		node = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatInt(v, 10)}
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, errors.New("non-finite numbers are not supported")
		}
		node = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: strconv.FormatFloat(v, 'g', -1, 64)}
	case nil:
		node = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	case []any:
		node = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
		for _, item := range v {
			child, err := valueNode(item)
			if err != nil {
				return nil, err
			}
			if child.Kind == yaml.SequenceNode {
				return nil, errors.New("nested arrays are not supported")
			}
			node.Content = append(node.Content, child)
		}
	default:
		return nil, fmt.Errorf("unsupported value type %T", value)
	}
	return &node, nil
}

func ValueYAML(value any) ([]byte, error) {
	node, err := valueNode(value)
	if err != nil {
		return nil, err
	}
	var encoded bytes.Buffer
	encoder := yaml.NewEncoder(&encoded)
	encoder.SetIndent(2)
	if err := encoder.Encode(node); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

func mappingKeys(mapping *yaml.Node) map[string]struct{} {
	keys := make(map[string]struct{}, len(mapping.Content)/2)
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		keys[mapping.Content[i].Value] = struct{}{}
	}
	return keys
}

func setNode(mapping *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			old := mapping.Content[i+1]
			value.HeadComment, value.LineComment, value.FootComment = old.HeadComment, old.LineComment, old.FootComment
			mapping.Content[i+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

func deleteNode(mapping *yaml.Node, key string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return
		}
	}
}

func FirstHeading(body []byte) string {
	start, _, title, _ := headingLocation(body)
	if start < 0 {
		return ""
	}
	return title
}

func ReplaceTitle(body []byte, title string) []byte {
	lines := strings.SplitAfter(string(body), "\n")
	start, underline, _, setext := headingLocation(body)
	if start < 0 {
		return []byte("# " + title + "\n\n" + string(body))
	}
	var result strings.Builder
	for index, raw := range lines {
		if index == underline && setext {
			continue
		}
		if index == start {
			line := strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")
			prefix := ""
			if setext {
				prefix = line[:len(line)-len(strings.TrimLeft(line, " "))]
			} else {
				prefix = line[:strings.Index(line, "#")]
			}
			ending := ""
			if strings.HasSuffix(raw, "\r\n") {
				ending = "\r\n"
			} else if strings.HasSuffix(raw, "\n") {
				ending = "\n"
			}
			result.WriteString(prefix + "# " + title + ending)
			continue
		}
		result.WriteString(raw)
	}
	return []byte(result.String())
}

func headingLocation(body []byte) (start, underline int, title string, setext bool) {
	lines := strings.SplitAfter(string(body), "\n")
	previousIndex := -1
	previousText := ""
	var fence byte
	fenceLength := 0
	for index, raw := range lines {
		line := strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")
		trimmed := strings.TrimSpace(line)
		if fence == 0 && (strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")) {
			fence = trimmed[0]
			fenceLength = len(trimmed) - len(strings.TrimLeft(trimmed, string(fence)))
			previousIndex = -1
			continue
		}
		if fence != 0 {
			if len(trimmed) >= fenceLength && len(strings.TrimLeft(trimmed, string(fence))) == 0 {
				fence = 0
			}
			previousIndex = -1
			continue
		}
		if level, heading := parseATXHeading(line); level == 1 {
			return index, index, heading, false
		}
		if isSetextH1(trimmed) && previousIndex >= 0 {
			return previousIndex, index, strings.TrimSpace(previousText), true
		}
		if trimmed == "" {
			previousIndex = -1
			previousText = ""
			continue
		}
		previousIndex = index
		previousText = line
	}
	return -1, -1, "", false
}

func isSetextH1(line string) bool {
	if line == "" {
		return false
	}
	for _, char := range line {
		if char != '=' {
			return false
		}
	}
	return true
}

func parseATXHeading(line string) (int, string) {
	leadingSpaces := 0
	for leadingSpaces < len(line) && line[leadingSpaces] == ' ' {
		leadingSpaces++
	}
	if leadingSpaces > 3 {
		return 0, ""
	}
	trimmed := strings.TrimSpace(line[leadingSpaces:])
	i := 0
	for i < len(trimmed) && trimmed[i] == '#' {
		i++
	}
	if i == 0 || i > 6 || i >= len(trimmed) || !unicode.IsSpace(rune(trimmed[i])) {
		return 0, ""
	}
	title := strings.TrimSpace(trimmed[i:])
	if lastSpace := strings.LastIndexAny(title, " \t"); lastSpace >= 0 {
		closing := strings.TrimSpace(title[lastSpace:])
		if closing != "" && strings.Trim(closing, "#") == "" {
			title = strings.TrimSpace(title[:lastSpace])
		}
	}
	return i, title
}

func Slug(title string) string {
	var b strings.Builder
	separator := false
	for _, r := range strings.ToLower(title) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if separator && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			separator = false
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) {
			// Preserve common non-ASCII titles as valid UTF-8 filename characters.
			if separator && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			separator = false
		} else {
			separator = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func FormatNumber(number int) string { return fmt.Sprintf("%03d", number) }

func ParseNumber(handle string) (int, error) {
	if handle == "" {
		return 0, errors.New("empty task handle")
	}
	for _, r := range handle {
		if !unicode.IsDigit(r) {
			return 0, errors.New("task handle is not a number")
		}
	}
	value, err := strconv.Atoi(handle)
	if err != nil || value < 1 {
		return 0, errors.New("task number must be positive")
	}
	return value, nil
}

func (t *Task) Handle() string { return FormatNumber(t.Number) }

func (t *Task) Clone() *Task {
	copyTask := *t
	copyTask.Metadata = make(map[string]any, len(t.Metadata))
	for key, value := range t.Metadata {
		copyTask.Metadata[key] = value
	}
	if t.frontMatter != nil {
		copyTask.frontMatter = cloneNode(t.frontMatter)
	}
	return &copyTask
}

func cloneNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	copyNode := *node
	copyNode.Content = make([]*yaml.Node, len(node.Content))
	for i, child := range node.Content {
		copyNode.Content[i] = cloneNode(child)
	}
	return &copyNode
}

func DecodeJSONValue(raw string) (any, error) {
	var value any
	decoder := jsonDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("provide exactly one JSON value")
	}
	return normalizeJSON(value)
}

// jsonDecoder is split out to keep task values on the same strict JSON path for
// metadata writes and filters.
func jsonDecoder(r io.Reader) *json.Decoder { return json.NewDecoder(r) }

func normalizeJSON(value any) (any, error) {
	switch v := value.(type) {
	case nil, bool, string:
		return v, nil
	case json.Number:
		if strings.ContainsAny(string(v), ".eE") {
			f, err := v.Float64()
			if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
				return nil, errors.New("invalid number")
			}
			return f, nil
		}
		i, err := v.Int64()
		if err != nil {
			return nil, errors.New("integer is out of range")
		}
		return i, nil
	case []any:
		items := make([]any, 0, len(v))
		for _, item := range v {
			normalized, err := normalizeJSON(item)
			if err != nil {
				return nil, err
			}
			if _, ok := normalized.([]any); ok {
				return nil, errors.New("metadata arrays cannot be nested")
			}
			items = append(items, normalized)
		}
		return items, nil
	case map[string]any:
		return nil, errors.New("metadata objects are not supported; use flat values or arrays")
	default:
		return nil, fmt.Errorf("unsupported JSON value %T", value)
	}
}
