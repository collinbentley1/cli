package main

import "testing"

func TestMainPackageSmoke(t *testing.T) {
	if got := 1 + 1; got != 2 {
		t.Fatalf("unexpected result: got %d", got)
	}
}
