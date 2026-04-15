package common

import (
	"context"
	"testing"
	"time"
)

func TestChannelConcurrencyUnlimited(t *testing.T) {
	lease, err := AcquireChannelConcurrency(context.Background(), 1001, 0)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	stats := GetChannelConcurrencyStats(1001, 0)
	if stats.InFlight != 0 || stats.Queued != 0 {
		t.Fatalf("expected unlimited channel not tracked, got %+v", stats)
	}
	lease.Release()
}

func TestChannelConcurrencyQueueAndRelease(t *testing.T) {
	DeleteChannelConcurrencyState(1002)
	firstLease, err := AcquireChannelConcurrency(context.Background(), 1002, 1)
	if err != nil {
		t.Fatalf("expected first acquire success, got %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		secondLease, secondErr := AcquireChannelConcurrency(context.Background(), 1002, 1)
		if secondErr != nil {
			t.Errorf("expected queued acquire success, got %v", secondErr)
			return
		}
		close(acquired)
		secondLease.Release()
	}()

	time.Sleep(50 * time.Millisecond)
	stats := GetChannelConcurrencyStats(1002, 1)
	if stats.InFlight != 1 || stats.Queued != 1 {
		t.Fatalf("expected 1 in flight and 1 queued, got %+v", stats)
	}

	firstLease.Release()

	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("expected queued request to acquire after release")
	}
}

func TestChannelConcurrencyCancelWhileQueued(t *testing.T) {
	DeleteChannelConcurrencyState(1003)
	firstLease, err := AcquireChannelConcurrency(context.Background(), 1003, 1)
	if err != nil {
		t.Fatalf("expected first acquire success, got %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, acquireErr := AcquireChannelConcurrency(ctx, 1003, 1)
		done <- acquireErr
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case acquireErr := <-done:
		if acquireErr == nil {
			t.Fatal("expected acquire to be canceled")
		}
	case <-time.After(time.Second):
		t.Fatal("expected canceled waiter to exit")
	}

	stats := GetChannelConcurrencyStats(1003, 1)
	if stats.Queued != 0 {
		t.Fatalf("expected queued count to return to 0, got %+v", stats)
	}
	firstLease.Release()
}

func TestChannelConcurrencyReleaseIdempotent(t *testing.T) {
	DeleteChannelConcurrencyState(1004)
	lease, err := AcquireChannelConcurrency(context.Background(), 1004, 1)
	if err != nil {
		t.Fatalf("expected acquire success, got %v", err)
	}
	lease.Release()
	lease.Release()
	stats := GetChannelConcurrencyStats(1004, 1)
	if stats.InFlight != 0 || stats.Queued != 0 {
		t.Fatalf("expected counters reset after idempotent release, got %+v", stats)
	}
}

func TestChannelConcurrencyDynamicLimit(t *testing.T) {
	DeleteChannelConcurrencyState(1005)
	firstLease, err := AcquireChannelConcurrency(context.Background(), 1005, 1)
	if err != nil {
		t.Fatalf("expected acquire success, got %v", err)
	}
	secondLease, err := AcquireChannelConcurrency(context.Background(), 1005, 2)
	if err != nil {
		t.Fatalf("expected acquire success after increasing limit, got %v", err)
	}
	stats := GetChannelConcurrencyStats(1005, 2)
	if stats.InFlight != 2 || stats.Queued != 0 {
		t.Fatalf("expected updated limit to allow second acquire, got %+v", stats)
	}
	secondLease.Release()
	firstLease.Release()
}
