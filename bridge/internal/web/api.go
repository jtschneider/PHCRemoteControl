package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/jtschneider/PHCRemoteControl/bridge/internal/controller"
	"github.com/jtschneider/PHCRemoteControl/bridge/internal/domain"
)

type statusResponse struct {
	Revision   uint64                      `json:"revision"`
	Connection controller.ConnectionStatus `json:"connection"`
	Stale      bool                        `json:"stale"`
	Project    string                      `json:"project"`
}

type projectResponse struct {
	Name   string     `json:"name"`
	Floors []apiFloor `json:"floors"`
}

type apiFloor struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Devices []apiDevice `json:"devices"`
}

type apiDevice struct {
	ID           string                  `json:"id"`
	Name         string                  `json:"name"`
	Kind         domain.DeviceKind       `json:"kind"`
	Category     string                  `json:"category"`
	Capabilities []controller.Capability `json:"capabilities"`
}

type commandRequest struct {
	Action controller.Action `json:"action"`
}

type reloadResponse struct {
	Reloaded bool `json:"reloaded"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func (s *Server) statusAPI(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodGet) {
		return
	}
	backend := s.current()
	snapshot := backend.Snapshot()
	writeJSON(w, http.StatusOK, statusResponse{Revision: snapshot.Revision,
		Connection: snapshot.Connection, Stale: snapshot.Stale, Project: backend.Project().Name})
}

func (s *Server) stateAPI(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, s.current().Snapshot())
}

func (s *Server) projectAPI(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodGet) {
		return
	}
	backend := s.current()
	project := backend.Project()
	response := projectResponse{Name: project.Name, Floors: make([]apiFloor, 0, len(project.Floors))}
	for _, floor := range project.Floors {
		apiFloor := apiFloor{ID: floor.ID, Name: floor.Name, Devices: make([]apiDevice, 0, len(floor.Devices))}
		for _, device := range floor.Devices {
			capabilities, err := backend.Capabilities(device.ID)
			if err != nil {
				continue
			}
			apiFloor.Devices = append(apiFloor.Devices, apiDevice{ID: device.ID, Name: device.Name,
				Kind: device.Kind, Category: device.Category, Capabilities: capabilities})
		}
		response.Floors = append(response.Floors, apiFloor)
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) commandAPI(w http.ResponseWriter, r *http.Request) {
	if !s.requireMutation(w, r) {
		return
	}
	prefix := "/api/v1/devices/"
	remainder := strings.TrimPrefix(r.URL.Path, prefix)
	if !strings.HasSuffix(remainder, "/commands") {
		http.NotFound(w, r)
		return
	}
	deviceID := strings.TrimSuffix(remainder, "/commands")
	if deviceID == "" || strings.Contains(deviceID, "/") {
		http.NotFound(w, r)
		return
	}
	var request commandRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON request"})
		return
	}
	result, err := s.current().Execute(r.Context(), deviceID, request.Action)
	if err != nil {
		s.writeControllerError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) reloadAPI(w http.ResponseWriter, r *http.Request) {
	if !s.requireMutation(w, r) {
		return
	}
	var body struct{}
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON request"})
		return
	}
	if err := s.Reload(r.Context()); err != nil {
		s.logger.Warn("project reload failed", "error", err)
		writeJSON(w, statusForError(err), errorResponse{Error: "project reload failed"})
		return
	}
	writeJSON(w, http.StatusOK, reloadResponse{Reloaded: true})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request must contain one JSON value")
	}
	return nil
}

func (s *Server) writeControllerError(w http.ResponseWriter, err error) {
	writeJSON(w, statusForError(err), errorResponse{Error: publicError(err)})
}

func statusForError(err error) int {
	switch {
	case errors.Is(err, controller.ErrDeviceNotFound):
		return http.StatusNotFound
	case errors.Is(err, controller.ErrUnsupportedAction):
		return http.StatusConflict
	case errors.Is(err, controller.ErrControllerStopped):
		return http.StatusServiceUnavailable
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout
	default:
		return http.StatusServiceUnavailable
	}
}

func publicError(err error) string {
	switch {
	case errors.Is(err, controller.ErrDeviceNotFound):
		return "device not found"
	case errors.Is(err, controller.ErrUnsupportedAction):
		return "action unsupported by device"
	case errors.Is(err, context.DeadlineExceeded):
		return "STM operation timed out"
	default:
		return "STM unavailable"
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
