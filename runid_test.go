package main

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestRunIDIs26LowercaseCharacters(t *testing.T) {
	id := newRunID(time.Now())
	if !regexp.MustCompile(`^[0-9a-z]{26}$`).MatchString(id) {
		t.Fatalf("run id %q is not 26 characters from [0-9a-z]", id)
	}
}

func TestRunIDsSortInTimeOrder(t *testing.T) {
	t0 := time.UnixMilli(1_760_000_000_000)
	first, second := newRunID(t0), newRunID(t0.Add(time.Millisecond))
	if first >= second {
		t.Fatalf("run id %q made 1 ms before %q does not sort first", first, second)
	}
}

func TestRunIDStartsWithTheTimeInCrockfordBase32(t *testing.T) {
	if id := newRunID(time.UnixMilli(1)); !strings.HasPrefix(id, "0000000001") {
		t.Fatalf("run id at 1 ms is %q, want prefix 0000000001", id)
	}
	if id := newRunID(time.UnixMilli(1<<48 - 1)); !strings.HasPrefix(id, "7zzzzzzzzz") {
		t.Fatalf("run id at the largest ULID time is %q, want prefix 7zzzzzzzzz", id)
	}
}
