package task

import (
	"strings"
	"testing"
	"time"
)

func TestMetadataFrontMatterRoundTrip(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 30, 0, 0, time.UTC)
	metadata := map[string]any{
		"priority": int64(3),
		"enabled":  true,
		"note":     "yes",
		"optional": nil,
		"labels":   []any{"mobile", "images", int64(2)},
	}
	task := New("wft_01K5Z4KJ6M2V7D9X3R8T5Q1P0A", 7, 2, "Image management", Todo, now, metadata)
	task.SetPath(Todo, task.Title)
	encoded, err := task.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(encoded, Todo, task.Path)
	if err != nil {
		t.Fatalf("Parse(Marshal(task)): %v\n%s", err, encoded)
	}
	if parsed.Title != task.Title || parsed.Number != task.Number || parsed.Order != task.Order {
		t.Fatalf("core fields changed during round trip: %#v", parsed)
	}
	if parsed.Metadata["priority"] != int64(3) || parsed.Metadata["enabled"] != true || parsed.Metadata["note"] != "yes" || parsed.Metadata["optional"] != nil {
		t.Fatalf("scalar metadata changed: %#v", parsed.Metadata)
	}
	labels, ok := parsed.Metadata["labels"].([]any)
	if !ok || len(labels) != 3 || labels[0] != "mobile" || labels[2] != int64(2) {
		t.Fatalf("array metadata changed: %#v", parsed.Metadata["labels"])
	}
	if !strings.Contains(string(encoded), `labels: [mobile, images, 2]`) {
		t.Fatalf("arrays should use compact flow style, got:\n%s", encoded)
	}
}

func TestMetadataJSONValueRejectsObjectsAndNestedArrays(t *testing.T) {
	for _, raw := range []string{`{"a":1}`, `[[1,2]]`, `"a" "b"`} {
		if _, err := DecodeJSONValue(raw); err == nil {
			t.Errorf("DecodeJSONValue(%q) unexpectedly succeeded", raw)
		}
	}
	value, err := DecodeJSONValue(`["ready", false, null, 4]`)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := value.([]any); !ok {
		t.Fatalf("expected array, got %T", value)
	}
}

func TestFirstHeadingIgnoresFencedMarkdownAndClosingHashes(t *testing.T) {
	body := []byte("```markdown\n# not the title\n```\n# Real C# title ###\n")
	if got := FirstHeading(body); got != "Real C# title" {
		t.Fatalf("FirstHeading() = %q", got)
	}
	replaced := ReplaceTitle(body, "New title")
	if got := FirstHeading(replaced); got != "New title" {
		t.Fatalf("ReplaceTitle() heading = %q", got)
	}
}

func TestFirstHeadingSupportsSetextH1(t *testing.T) {
	body := []byte("Context before\n\nProduct bundles\n===============\n")
	if got := FirstHeading(body); got != "Product bundles" {
		t.Fatalf("FirstHeading() = %q", got)
	}
	replaced := ReplaceTitle(body, "Bundle pricing")
	if got := FirstHeading(replaced); got != "Bundle pricing" {
		t.Fatalf("ReplaceTitle() heading = %q", got)
	}
	if strings.Contains(string(replaced), "===============") {
		t.Fatalf("setext underline should be removed when title is renamed:\n%s", replaced)
	}
}
