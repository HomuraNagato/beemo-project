package main

import "testing"

func TestTruncateCoreContext(t *testing.T) {
	if got := truncateCoreContext("abcdef", 4); got != "abcd" {
		t.Fatalf("truncateCoreContext = %q, want abcd", got)
	}
	if got := truncateCoreContext("abcdef", 0); got != "abcdef" {
		t.Fatalf("zero limit should preserve context, got %q", got)
	}
	if got := truncateCoreContext("abc日本語", 5); got != "abc日本" {
		t.Fatalf("unicode context was not truncated by characters: %q", got)
	}
}
