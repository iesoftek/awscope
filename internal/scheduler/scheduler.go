package scheduler

import (
	"context"
	"sync"
)

type Key struct {
	ProviderID string
	Profile    string
	AccountID  string
	Partition  string
	Region     string
	Kind       string
}

type Job struct {
	Key Key
	Run func(ctx context.Context) error

	gen uint64
}

type Scheduler struct {
	baseCtx context.Context

	mu      sync.Mutex
	gen     uint64
	ctx     context.Context
	cancel  context.CancelFunc
	closing bool

	queue chan Job

	pending map[Key]struct{}
	wg      sync.WaitGroup
}

type Options struct {
	QueueSize int
	Workers   int
}

func New(baseCtx context.Context, opts Options) *Scheduler {
	if opts.QueueSize <= 0 {
		opts.QueueSize = 128
	}
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	ctx, cancel := context.WithCancel(baseCtx)
	s := &Scheduler{
		baseCtx: baseCtx,
		ctx:     ctx,
		cancel:  cancel,
		queue:   make(chan Job, opts.QueueSize),
		pending: map[Key]struct{}{},
		closing: false,
		gen:     1,
	}

	for i := 0; i < opts.Workers; i++ {
		s.wg.Add(1)
		go s.worker()
	}

	return s
}

// Reset cancels all in-flight jobs (best-effort) and bumps generation so queued jobs from
// the previous generation are dropped.
func (s *Scheduler) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return
	}
	s.gen++
	s.cancel()
	s.ctx, s.cancel = context.WithCancel(s.baseCtx)
	// Pending keys remain, but generation drop ensures they won't run; allow resubmits.
	s.pending = map[Key]struct{}{}
}

func (s *Scheduler) Close() {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return
	}
	s.closing = true
	s.cancel()
	close(s.queue)
	s.mu.Unlock()

	s.wg.Wait()
}

// Submit enqueues the job if no job with the same key is currently pending.
// Returns true if enqueued.
func (s *Scheduler) Submit(job Job) bool {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return false
	}
	if _, ok := s.pending[job.Key]; ok {
		s.mu.Unlock()
		return false
	}
	s.pending[job.Key] = struct{}{}
	job.gen = s.gen
	s.mu.Unlock()

	select {
	case s.queue <- job:
		return true
	case <-s.ctx.Done():
		// Scheduler generation canceled; treat as not enqueued.
		s.mu.Lock()
		delete(s.pending, job.Key)
		s.mu.Unlock()
		return false
	}
}

func (s *Scheduler) worker() {
	defer s.wg.Done()
	for job := range s.queue {
		s.mu.Lock()
		curGen := s.gen
		ctx := s.ctx
		s.mu.Unlock()

		// Drop jobs from older generations.
		if job.gen != curGen {
			s.mu.Lock()
			delete(s.pending, job.Key)
			s.mu.Unlock()
			continue
		}

		_ = job.Run(ctx)

		s.mu.Lock()
		delete(s.pending, job.Key)
		s.mu.Unlock()
	}
}
