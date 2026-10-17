package main

import (
	"fmt"
	"net/http/httptest"
	"strings"
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

func TestTurnCreds(t *testing.T) {
	turnURL = "turn:x"
	defer func() { turnURL = "" }()
	old, now := time.Now().Add(-time.Hour), time.Now()
	a, b := &peer{id: "a", room: "1", joined: old}, &peer{id: "b", room: "1", joined: now}
	rooms["1"], tokens["ta"] = map[string]*peer{"a": a, "b": b}, a
	defer clear(rooms)
	defer clear(tokens)
	defer clear(creds)
	get := func() int {
		w := httptest.NewRecorder()
		turnCreds(w, httptest.NewRequest("POST", "/api/turn", strings.NewReader(`{"token":"ta"}`)))
		return w.Code
	}
	if c := get(); c != 200 {
		t.Errorf("b just joined, a should get a bridge: %d", c)
	}
	b.joined = old
	if c := get(); c != 403 {
		t.Errorf("past the call's start, no bridge: %d", c)
	}
	b.joined = now
	for range maxCredsPerMin {
		get()
	}
	if c := get(); c != 429 {
		t.Errorf("past the per-IP cap: %d", c)
	}
}
