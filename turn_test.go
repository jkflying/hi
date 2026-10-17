package main

import (
	"fmt"
	"testing"
	"time"
)

func TestTurnKey(t *testing.T) {
	rooms["123456"] = map[string]*peer{"a": {}, "b": {}}
	rooms["654321"] = map[string]*peer{"c": {}}
	defer clear(rooms)
	future, past := time.Now().Add(time.Hour).Unix(), time.Now().Add(-time.Second).Unix()
	for user, want := range map[string]bool{
		fmt.Sprintf("%d:123456:a", future): true,
		fmt.Sprintf("%d:123456:a", past):   false, // expired
		fmt.Sprintf("%d:123456:z", future): false, // not in the room
		fmt.Sprintf("%d:654321:c", future): false, // alone, nobody to call
		"garbage":                          false,
	} {
		if _, ok := turnKey(user, "hi"); ok != want {
			t.Errorf("%s: got %v", user, ok)
		}
	}
	b := &bucket{rate: 1000, burst: 1000}
	if !b.take(1000) || b.take(500) || !func() bool { time.Sleep(600 * time.Millisecond); return b.take(500) }() {
		t.Error("bucket should start full, then refill at its rate")
	}
}
