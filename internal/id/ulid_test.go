package id

import (
	"regexp"
	"testing"
	"time"
)

func TestNewProducesPrefixedULID(t *testing.T) {
	got, err := New(time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^tuck_[0-7][0-9A-HJKMNP-TV-Z]{25}$`).MatchString(got) {
		t.Fatalf("invalid ULID %q", got)
	}
}
