package controller

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestSchedulerCommandsPrecedeQueuedPolls(t *testing.T) {
	scheduler := newScheduler(8)
	defer scheduler.close()

	var mu sync.Mutex
	var order []string
	record := func(value string) {
		mu.Lock()
		order = append(order, value)
		mu.Unlock()
	}

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		_, err := scheduler.poll(context.Background(), func(ctx context.Context) (any, error) {
			record("poll-1-start")
			close(firstStarted)
			select {
			case <-releaseFirst:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			record("poll-1-end")
			return nil, nil
		})
		firstDone <- err
	}()
	<-firstStarted

	secondDone := make(chan error, 1)
	go func() {
		_, err := scheduler.poll(context.Background(), func(context.Context) (any, error) {
			record("poll-2")
			return nil, nil
		})
		secondDone <- err
	}()
	waitFor(t, time.Second, func() bool { return len(scheduler.background) == 1 })

	commandDone := make(chan error, 1)
	go func() {
		_, err := scheduler.command(context.Background(), func(context.Context) (any, error) {
			record("command")
			return nil, nil
		})
		commandDone <- err
	}()
	waitFor(t, time.Second, func() bool { return len(scheduler.commands) == 1 })
	close(releaseFirst)

	for _, result := range []<-chan error{firstDone, commandDone, secondDone} {
		if err := <-result; err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"poll-1-start", "poll-1-end", "command", "poll-2"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("execution order = %v, want %v", order, want)
	}
}

func TestSchedulerCloseCancelsInflightAndQueuedWork(t *testing.T) {
	scheduler := newScheduler(4)
	started := make(chan struct{})
	inflight := make(chan error, 1)
	go func() {
		_, err := scheduler.poll(context.Background(), func(ctx context.Context) (any, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		inflight <- err
	}()
	<-started

	queued := make(chan error, 1)
	go func() {
		_, err := scheduler.command(context.Background(), func(context.Context) (any, error) {
			return nil, errors.New("queued command should not run")
		})
		queued <- err
	}()
	waitFor(t, time.Second, func() bool { return len(scheduler.commands) == 1 })
	scheduler.close()

	if err := <-inflight; !errors.Is(err, ErrSchedulerStopped) && !errors.Is(err, context.Canceled) {
		t.Fatalf("inflight error = %v", err)
	}
	if err := <-queued; !errors.Is(err, ErrSchedulerStopped) {
		t.Fatalf("queued error = %v, want scheduler stopped", err)
	}
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}
