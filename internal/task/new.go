package task

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

func New(id string, number, order int, title string, state State, now time.Time, metadata map[string]any) *Task {
	t := &Task{
		ID: id, Number: number, Order: order, State: state,
		CreatedAt: now.UTC().Format(time.RFC3339Nano),
		Title: title, Body: "# " + title + "\n\n",
		Metadata: make(map[string]any),
		frontMatter: &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}},
	}
	if state == Done {
		t.CompletedAt = t.CreatedAt
		t.Order = 0
	}
	for key, value := range metadata {
		t.Metadata[key] = value
	}
	return t
}

func (t *Task) SetPath(state State, title string) {
	t.State = state
	t.Title = title
	slug := Slug(title)
	if slug == "" {
		slug = "task"
	}
	t.Path = fmt.Sprintf("tasks/%s/%s-%s.md", state, FormatNumber(t.Number), slug)
}
