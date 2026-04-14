package config

import "testing"

func TestDownstreamCallIsAsync(t *testing.T) {
	if (DownstreamCall{Mode: "async"}).IsAsync() != true {
		t.Fatal("expected async")
	}
	if (DownstreamCall{Mode: "event"}).IsAsync() != true {
		t.Fatal("expected event")
	}
	if (DownstreamCall{Mode: "sync"}).IsAsync() {
		t.Fatal("expected sync not async")
	}
	if (DownstreamCall{}).IsAsync() {
		t.Fatal("empty mode defaults to sync")
	}
}
