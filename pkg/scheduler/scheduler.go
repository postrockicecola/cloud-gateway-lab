package scheduler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

type Priority int

const (
	PriorityLow Priority = iota
	PriorityHigh
)

// Job is a unit of work that occupies one downstream worker slot.
type Job func(ctx context.Context)

type jobRequest struct {
	ctx      context.Context
	job      Job
	done     chan struct{}
	canceled atomic.Bool
}

// Scheduler queues requests by priority and caps concurrent upstream calls.
type Scheduler struct {
	highPriorityChan chan *jobRequest
	lowPriorityChan  chan *jobRequest
	workerSem        chan struct{}
	stop             chan struct{}
	wg               sync.WaitGroup
}

func New(maxConcurrency, queueSize int) (*Scheduler, error) {
	if maxConcurrency < 1 {
		return nil, errors.New("maxConcurrency must be positive")
	}
	if queueSize < 1 {
		return nil, errors.New("queueSize must be positive")
	}
	s := &Scheduler{
		highPriorityChan: make(chan *jobRequest, queueSize),
		lowPriorityChan:  make(chan *jobRequest, queueSize),
		workerSem:        make(chan struct{}, maxConcurrency),
		stop:             make(chan struct{}),
	}
	s.wg.Add(1)
	go s.dispatch()
	return s, nil
}

// Submit enqueues req. When every worker slot is busy the request waits in
// the priority channel; the dispatcher always drains highPriorityChan first.
func (s *Scheduler) Submit(ctx context.Context, req Job, priority Priority) error {
	if req == nil {
		return errors.New("job is required")
	}
	jr := &jobRequest{
		ctx:  ctx,
		job:  req,
		done: make(chan struct{}),
	}
	queue := s.lowPriorityChan
	if priority == PriorityHigh {
		queue = s.highPriorityChan
	}

	select {
	case queue <- jr:
	case <-ctx.Done():
		return ctx.Err()
	case <-s.stop:
		return errors.New("scheduler stopped")
	}

	select {
	case <-jr.done:
		return nil
	case <-ctx.Done():
		jr.canceled.Store(true)
		return ctx.Err()
	case <-s.stop:
		return errors.New("scheduler stopped")
	}
}

func (s *Scheduler) Stop() {
	select {
	case <-s.stop:
		return
	default:
		close(s.stop)
	}
	s.wg.Wait()
}

func (s *Scheduler) dispatch() {
	defer s.wg.Done()
	for {
		// Reserve a worker slot first so queued jobs stay in their
		// priority channels until a downstream slot is actually free.
		select {
		case <-s.stop:
			return
		case s.workerSem <- struct{}{}:
		}

		jr, ok := s.tryDequeue()
		if !ok {
			<-s.workerSem
			jr, ok = s.dequeue()
			if !ok {
				return
			}
			select {
			case <-s.stop:
				close(jr.done)
				return
			case s.workerSem <- struct{}{}:
			}
		}

		if jr.canceled.Load() || jr.ctx.Err() != nil {
			<-s.workerSem
			close(jr.done)
			continue
		}

		s.wg.Add(1)
		go s.run(jr)
	}
}

func (s *Scheduler) run(jr *jobRequest) {
	defer s.wg.Done()
	defer func() { <-s.workerSem }()
	defer close(jr.done)
	if jr.canceled.Load() || jr.ctx.Err() != nil {
		return
	}
	jr.job(jr.ctx)
}

func (s *Scheduler) tryDequeue() (*jobRequest, bool) {
	select {
	case jr := <-s.highPriorityChan:
		return jr, true
	default:
	}
	select {
	case jr := <-s.highPriorityChan:
		return jr, true
	case jr := <-s.lowPriorityChan:
		return jr, true
	default:
		return nil, false
	}
}

// dequeue prefers VIP / short-text jobs, then falls back to the low queue.
func (s *Scheduler) dequeue() (*jobRequest, bool) {
	select {
	case <-s.stop:
		return nil, false
	case jr := <-s.highPriorityChan:
		return jr, true
	default:
	}

	select {
	case <-s.stop:
		return nil, false
	case jr := <-s.highPriorityChan:
		return jr, true
	case jr := <-s.lowPriorityChan:
		return jr, true
	}
}
