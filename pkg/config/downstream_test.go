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

func TestDownstreamCallIsRetryable(t *testing.T) {
	if !(DownstreamCall{}).IsRetryable() {
		t.Fatal("default retryable true")
	}
	f := false
	if (DownstreamCall{Retryable: &f}).IsRetryable() {
		t.Fatal("expected retryable false")
	}
	tv := true
	if !(DownstreamCall{Retryable: &tv}).IsRetryable() {
		t.Fatal("expected retryable true")
	}
}
