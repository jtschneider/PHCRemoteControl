package controller

import (
	"context"
	"errors"
	"sync"
)

var ErrSchedulerStopped = errors.New("controller: scheduler stopped")

type scheduledOperation func(context.Context) (any, error)

type scheduledResult struct {
	value any
	err   error
}

type scheduledRequest struct {
	ctx    context.Context
	run    scheduledOperation
	result chan scheduledResult
}

// scheduler is the single ownership point for STM calls. A command sequence is
// one high-priority operation; each module poll is one background operation.
type scheduler struct {
	ctx        context.Context
	cancel     context.CancelFunc
	commands   chan scheduledRequest
	background chan scheduledRequest
	done       chan struct{}
	closeOnce  sync.Once
}

func newScheduler(queueDepth int) *scheduler {
	if queueDepth < 1 {
		queueDepth = 64
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &scheduler{
		ctx:        ctx,
		cancel:     cancel,
		commands:   make(chan scheduledRequest, queueDepth),
		background: make(chan scheduledRequest, queueDepth),
		done:       make(chan struct{}),
	}
	go s.run()
	return s
}

func (s *scheduler) command(ctx context.Context, operation scheduledOperation) (any, error) {
	return s.submit(ctx, s.commands, operation)
}

func (s *scheduler) poll(ctx context.Context, operation scheduledOperation) (any, error) {
	return s.submit(ctx, s.background, operation)
}

func (s *scheduler) submit(
	ctx context.Context,
	queue chan<- scheduledRequest,
	operation scheduledOperation,
) (any, error) {
	if operation == nil {
		return nil, errors.New("controller: nil scheduled operation")
	}
	request := scheduledRequest{
		ctx:    ctx,
		run:    operation,
		result: make(chan scheduledResult, 1),
	}
	select {
	case queue <- request:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.ctx.Done():
		return nil, ErrSchedulerStopped
	}

	select {
	case result := <-request.result:
		return result.value, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.ctx.Done():
		return nil, ErrSchedulerStopped
	}
}

func (s *scheduler) run() {
	defer close(s.done)
	for {
		request, ok := s.next()
		if !ok {
			s.drain()
			return
		}
		if err := request.ctx.Err(); err != nil {
			request.result <- scheduledResult{err: err}
			continue
		}

		opCtx, cancel := context.WithCancel(request.ctx)
		stopCancel := context.AfterFunc(s.ctx, cancel)
		value, err := request.run(opCtx)
		stopCancel()
		cancel()
		request.result <- scheduledResult{value: value, err: err}
	}
}

func (s *scheduler) next() (scheduledRequest, bool) {
	select {
	case <-s.ctx.Done():
		return scheduledRequest{}, false
	default:
	}

	// Always drain a command already in the queue before starting another poll.
	select {
	case request := <-s.commands:
		return request, true
	default:
	}

	select {
	case request := <-s.commands:
		return request, true
	case request := <-s.background:
		return request, true
	case <-s.ctx.Done():
		return scheduledRequest{}, false
	}
}

func (s *scheduler) drain() {
	for {
		select {
		case request := <-s.commands:
			request.result <- scheduledResult{err: ErrSchedulerStopped}
		case request := <-s.background:
			request.result <- scheduledResult{err: ErrSchedulerStopped}
		default:
			return
		}
	}
}

func (s *scheduler) close() {
	s.closeOnce.Do(s.cancel)
	<-s.done
}
