package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jtschneider/PHCRemoteControl/bridge/internal/controller"
)

func (s *Server) eventsAPI(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodGet) {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	backend := s.current()
	events, unsubscribe, err := backend.Subscribe(32)
	if err != nil {
		s.writeControllerError(w, err)
		return
	}
	defer unsubscribe()
	projectReload, removeNotifier := s.projectNotifier()
	defer removeNotifier()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	heartbeat := time.NewTicker(s.heartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-projectReload:
			_ = writeSSE(w, "project", map[string]any{"reloadRequired": true})
			flusher.Flush()
			return
		case event, open := <-events:
			if !open {
				select {
				case <-projectReload:
					_ = writeSSE(w, "project", map[string]any{"reloadRequired": true})
					flusher.Flush()
				default:
				}
				return
			}
			if err := writeControllerSSE(w, event); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeControllerSSE(w http.ResponseWriter, event controller.Event) error {
	switch event.Kind {
	case controller.EventSnapshot:
		return writeSSE(w, "snapshot", event.Snapshot)
	case controller.EventState:
		if event.State == nil {
			return nil
		}
		return writeSSE(w, "state", map[string]any{"revision": event.Revision,
			"deviceID": event.DeviceID, "power": event.State.Power})
	case controller.EventConnection:
		return writeSSE(w, "connection", map[string]any{"revision": event.Revision,
			"status": event.Connection, "stale": event.Stale})
	default:
		return nil
	}
}

func writeSSE(w http.ResponseWriter, event string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	return err
}

func (s *Server) projectNotifier() (<-chan struct{}, func()) {
	s.notifyMu.Lock()
	s.nextNotify++
	id := s.nextNotify
	channel := make(chan struct{}, 1)
	s.notifiers[id] = channel
	s.notifyMu.Unlock()
	return channel, func() {
		s.notifyMu.Lock()
		delete(s.notifiers, id)
		s.notifyMu.Unlock()
	}
}

func (s *Server) notifyProjectReload() {
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	for _, notifier := range s.notifiers {
		select {
		case notifier <- struct{}{}:
		default:
		}
	}
}
