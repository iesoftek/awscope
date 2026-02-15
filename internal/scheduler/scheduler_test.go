package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestScheduler_DedupesByKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := New(ctx, Options{Workers: 1, QueueSize: 1})
	defer s.Close()

	var ran atomic.Int32
	job := Job{
		Key: Key{ProviderID: "ec2", Kind: "list", Region: "us-east-1"},
		Run: func(ctx context.Context) error {
			ran.Add(1)
			return nil
		},
	}

	if !s.Submit(job) {
		t.Fatalf("expected first submit to enqueue")
	}
	if s.Submit(job) {
		t.Fatalf("expected second submit to be deduped")
	}

	// Allow worker to run.
	time.Sleep(50 * time.Millisecond)

	if got := ran.Load(); got != 1 {
		t.Fatalf("ran: got %d want 1", got)
	}
}

func TestScheduler_ResetCancelsJobs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := New(ctx, Options{Workers: 1, QueueSize: 8})
	defer s.Close()

	block := make(chan struct{})
	var sawCancel atomic.Bool

	if !s.Submit(Job{
		Key: Key{ProviderID: "ec2", Kind: "list", Region: "us-east-1"},
		Run: func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				sawCancel.Store(true)
			case <-block:
			}
			return nil
		},
	}) {
		t.Fatalf("submit failed")
	}

	// Give worker time to start and block.
	time.Sleep(50 * time.Millisecond)
	s.Reset()

	// Worker should observe cancellation quickly.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if sawCancel.Load() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected job to observe cancellation on Reset")
}
