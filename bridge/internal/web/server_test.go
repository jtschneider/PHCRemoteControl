package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jtschneider/PHCRemoteControl/bridge/internal/controller"
	"github.com/jtschneider/PHCRemoteControl/bridge/internal/domain"
)

type fakeBackend struct {
	mu         sync.Mutex
	project    domain.Project
	snapshot   controller.Snapshot
	commands   []commandRequest
	subscribe  []controller.Event
	executeErr error
	closed     bool
}

func (f *fakeBackend) Project() domain.Project       { return f.project }
func (f *fakeBackend) Snapshot() controller.Snapshot { return f.snapshot }
func (f *fakeBackend) Device(id string) (domain.Device, bool) {
	for _, floor := range f.project.Floors {
		for _, device := range floor.Devices {
			if device.ID == id {
				return device, true
			}
		}
	}
	return domain.Device{}, false
}
func (f *fakeBackend) Capabilities(id string) ([]controller.Capability, error) {
	device, ok := f.Device(id)
	if !ok {
		return nil, controller.ErrDeviceNotFound
	}
	switch device.Kind {
	case domain.KindLight, domain.KindOutlet:
		return []controller.Capability{{Action: controller.ActionOn}, {Action: controller.ActionOff}, {Action: controller.ActionToggle}}, nil
	case domain.KindShutter:
		return []controller.Capability{{Action: controller.ActionRaise}, {Action: controller.ActionStop}, {Action: controller.ActionLower},
			{Action: controller.ActionTiltOpen, Experimental: true}}, nil
	case domain.KindScene:
		return []controller.Capability{{Action: controller.ActionActivate}}, nil
	case domain.KindButton:
		return []controller.Capability{{Action: controller.ActionShortPress}, {Action: controller.ActionLongPress}}, nil
	default:
		return nil, nil
	}
}
func (f *fakeBackend) Execute(_ context.Context, id string, action controller.Action) (controller.CommandResult, error) {
	if f.executeErr != nil {
		return controller.CommandResult{}, f.executeErr
	}
	capabilities, err := f.Capabilities(id)
	if err != nil {
		return controller.CommandResult{}, err
	}
	for _, capability := range capabilities {
		if capability.Action == action {
			f.mu.Lock()
			f.commands = append(f.commands, commandRequest{Action: action})
			f.mu.Unlock()
			return controller.CommandResult{ID: "command-test"}, nil
		}
	}
	return controller.CommandResult{}, controller.ErrUnsupportedAction
}
func (f *fakeBackend) Subscribe(_ int) (<-chan controller.Event, func(), error) {
	channel := make(chan controller.Event, len(f.subscribe))
	for _, event := range f.subscribe {
		channel <- event
	}
	close(channel)
	return channel, func() {}, nil
}
func (f *fakeBackend) Close() { f.mu.Lock(); f.closed = true; f.mu.Unlock() }

func testBackend() *fakeBackend {
	project := domain.Project{Name: "A very long synthetic project name for the bridge", Floors: []domain.Floor{{
		ID: "floor:v1:test", Name: "3. Obergeschoss mit sehr langem Namen", Devices: []domain.Device{
			{ID: "device:v1:amd:1:2", Name: "Flur Deckenlicht", Kind: domain.KindLight, Category: "Licht", Ref: domain.ChannelRef{ModuleClass: domain.ModuleAMD, DIP: 1, Channel: 2}},
			{ID: "device:v1:emd:4:0", Name: "Terrasse", Kind: domain.KindShutter, Category: "Jalousie", Ref: domain.ChannelRef{ModuleClass: domain.ModuleEMD, DIP: 4, Channel: 0}},
			{ID: "device:v1:emd:7:1", Name: "Panik", Kind: domain.KindScene, Category: "Sicherheit", Ref: domain.ChannelRef{ModuleClass: domain.ModuleEMD, DIP: 7, Channel: 1}},
		},
	}}}
	snapshot := controller.Snapshot{Revision: 7, Connection: controller.ConnectionConnected, Devices: map[string]controller.DeviceState{
		"device:v1:amd:1:2": {Power: controller.PowerOn},
	}}
	return &fakeBackend{project: project, snapshot: snapshot}
}

func newTestServer(t *testing.T, backend *fakeBackend, reload ReloadFunc) *Server {
	t.Helper()
	server, err := New(backend, Config{PublicOrigin: "http://bridge.test", STMAddress: "stm.test:6680",
		Reload: reload, HeartbeatInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	return server
}

func request(t *testing.T, handler http.Handler, method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://bridge.test"+target, strings.NewReader(body))
	req.Host = "bridge.test"
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func TestFloorTemplateProvidesStableDOMContractInBothLanguages(t *testing.T) {
	server := newTestServer(t, testBackend(), nil)
	for _, lang := range []string{"en", "de"} {
		t.Run(lang, func(t *testing.T) {
			response := request(t, server.Handler(), http.MethodGet, "/floors/floor:v1:test?lang="+lang, "", nil)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body %s", response.Code, response.Body.String())
			}
			body := response.Body.String()
			for _, anchor := range []string{
				`data-device-id="device:v1:amd:1:2"`, `data-role="power-state"`,
				`data-role="power-command"`, `data-role="command-status"`, `data-action="toggle"`,
				`data-confirm=`, `data-category="Licht"`,
			} {
				if !strings.Contains(body, anchor) {
					t.Errorf("rendered page lacks %s", anchor)
				}
			}
			if lang == "de" && !strings.Contains(body, ">Hoch</span>") {
				t.Error("German action chrome was not rendered")
			}
		})
	}
}

func TestProjectAPIExposesCapabilitiesWithoutHardwareAddresses(t *testing.T) {
	server := newTestServer(t, testBackend(), nil)
	response := request(t, server.Handler(), http.MethodGet, "/api/v1/project", "", nil)
	if response.Code != http.StatusOK {
		t.Fatal(response.Code)
	}
	body := response.Body.String()
	for _, forbidden := range []string{"moduleClass", `"dip"`, `"channel"`, "upRef"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("API leaked hardware field %q", forbidden)
		}
	}
	if !strings.Contains(body, `"experimental":true`) {
		t.Error("experimental capability marker missing")
	}
}

func TestMutationSecurityAndCapabilityErrors(t *testing.T) {
	server := newTestServer(t, testBackend(), nil)
	handler := server.Handler()
	target := "/api/v1/devices/device:v1:amd:1:2/commands"

	missingOrigin := request(t, handler, http.MethodPost, target, `{"action":"toggle"}`, map[string]string{"Content-Type": "application/json"})
	if missingOrigin.Code != http.StatusForbidden {
		t.Errorf("missing Origin status = %d", missingOrigin.Code)
	}

	wrongType := request(t, handler, http.MethodPost, target, `{"action":"toggle"}`, map[string]string{"Origin": "http://bridge.test", "Content-Type": "text/plain"})
	if wrongType.Code != http.StatusUnsupportedMediaType {
		t.Errorf("wrong type status = %d", wrongType.Code)
	}

	validHeaders := map[string]string{"Origin": "http://bridge.test", "Content-Type": "application/json"}
	valid := request(t, handler, http.MethodPost, target, `{"action":"toggle"}`, validHeaders)
	if valid.Code != http.StatusAccepted {
		t.Fatalf("valid command status = %d, body %s", valid.Code, valid.Body.String())
	}

	unsupported := request(t, handler, http.MethodPost, target, `{"action":"raise"}`, validHeaders)
	if unsupported.Code != http.StatusConflict {
		t.Errorf("unsupported status = %d", unsupported.Code)
	}

	unknown := request(t, handler, http.MethodPost, "/api/v1/devices/missing/commands", `{"action":"toggle"}`, validHeaders)
	if unknown.Code != http.StatusNotFound {
		t.Errorf("unknown status = %d", unknown.Code)
	}

	unknownField := request(t, handler, http.MethodPost, target, `{"action":"toggle","raw":1}`, validHeaders)
	if unknownField.Code != http.StatusBadRequest {
		t.Errorf("unknown field status = %d", unknownField.Code)
	}
}

func TestHostAndResponseSecurityHeaders(t *testing.T) {
	server := newTestServer(t, testBackend(), nil)
	req := httptest.NewRequest(http.MethodGet, "http://attacker.test/", nil)
	req.Host = "attacker.test"
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("wrong Host status = %d", recorder.Code)
	}
	if recorder.Header().Get("Content-Security-Policy") == "" {
		t.Error("missing CSP")
	}
	if recorder.Header().Get("X-Frame-Options") != "DENY" {
		t.Error("missing frame denial")
	}
	if recorder.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("unexpected CORS header")
	}
}

func TestStaticAssetsUseStrongETags(t *testing.T) {
	server := newTestServer(t, testBackend(), nil)
	first := request(t, server.Handler(), http.MethodGet, "/static/app.js", "", nil)
	if first.Code != http.StatusOK {
		t.Fatal(first.Code)
	}
	etag := first.Header().Get("ETag")
	if len(etag) != 66 || !strings.HasPrefix(etag, `"`) {
		t.Fatalf("ETag = %q", etag)
	}
	second := request(t, server.Handler(), http.MethodGet, "/static/app.js", "", map[string]string{"If-None-Match": etag})
	if second.Code != http.StatusNotModified {
		t.Errorf("conditional status = %d", second.Code)
	}
}

func TestOriginalLogoFormatsAreServed(t *testing.T) {
	server := newTestServer(t, testBackend(), nil)
	for _, test := range []struct {
		path        string
		contentType string
	}{
		{"/static/logo.svg", "image/svg+xml"},
		{"/static/logo.png", "image/png"},
	} {
		response := request(t, server.Handler(), http.MethodGet, test.path, "", nil)
		if response.Code != http.StatusOK {
			t.Errorf("%s status = %d", test.path, response.Code)
		}
		if got := response.Header().Get("Content-Type"); got != test.contentType {
			t.Errorf("%s content type = %q, want %q", test.path, got, test.contentType)
		}
		if response.Body.Len() < 1024 {
			t.Errorf("%s appears truncated: %d bytes", test.path, response.Body.Len())
		}
	}

	page := request(t, server.Handler(), http.MethodGet, "/", "", nil).Body.String()
	if !strings.Contains(page, `rel="apple-touch-icon" href="/static/logo.png"`) {
		t.Error("page lacks PNG Apple touch icon")
	}
	if !strings.Contains(page, `href="/static/logo.svg" type="image/svg+xml"`) {
		t.Error("page lacks SVG browser icon")
	}
}

func TestShutterControlsKeepThreePrimaryActionsOnOneRow(t *testing.T) {
	server := newTestServer(t, testBackend(), nil)
	css := string(server.assets["app.css"].data)
	for _, rule := range []string{
		".device-shutter .device-controls { grid-template-columns: repeat(8, minmax(0, 1fr)); }",
		"grid-column: span 3;",
		".device-shutter .device-controls .action-stop { grid-column: span 2; }",
		"white-space: nowrap;",
	} {
		if !strings.Contains(css, rule) {
			t.Errorf("shutter layout CSS lacks %q", rule)
		}
	}
}

func TestFavouriteButtonKeepsSquareTouchTarget(t *testing.T) {
	server := newTestServer(t, testBackend(), nil)
	css := string(server.assets["app.css"].data)
	for _, rule := range []string{
		"align-self: start;",
		"width: 44px;",
		"height: 44px;",
		"aspect-ratio: 1;",
	} {
		if !strings.Contains(css, rule) {
			t.Errorf("favourite button CSS lacks %q", rule)
		}
	}
}

func TestSSEWritesSnapshotUsingWireContract(t *testing.T) {
	backend := testBackend()
	snapshot := backend.snapshot
	backend.subscribe = []controller.Event{{Kind: controller.EventSnapshot, Revision: snapshot.Revision, Snapshot: &snapshot}}
	server := newTestServer(t, backend, nil)
	response := request(t, server.Handler(), http.MethodGet, "/api/v1/events", "", nil)
	if response.Code != http.StatusOK {
		t.Fatal(response.Code)
	}
	if got := response.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("content type = %q", got)
	}
	body := response.Body.String()
	if !strings.Contains(body, "event: snapshot\n") || !strings.Contains(body, `"revision":7`) {
		t.Errorf("unexpected SSE body %q", body)
	}
}

func TestReloadSwapsControllersAndClosesPrevious(t *testing.T) {
	first := testBackend()
	second := testBackend()
	second.project.Name = "Reloaded"
	server := newTestServer(t, first, func(context.Context, Backend) (Backend, error) { return second, nil })
	response := request(t, server.Handler(), http.MethodPost, "/api/v1/project/reload", `{}`,
		map[string]string{"Origin": "http://bridge.test", "Content-Type": "application/json"})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", response.Code, response.Body.String())
	}
	first.mu.Lock()
	closed := first.closed
	first.mu.Unlock()
	if !closed {
		t.Error("previous controller was not closed")
	}
	if server.current().Project().Name != "Reloaded" {
		t.Error("new controller was not installed")
	}
}

func TestControllerErrorsDoNotLeakDetails(t *testing.T) {
	backend := testBackend()
	backend.executeErr = errors.New("private STM address and telegram details")
	server := newTestServer(t, backend, nil)
	response := request(t, server.Handler(), http.MethodPost, "/api/v1/devices/device:v1:amd:1:2/commands", `{"action":"toggle"}`,
		map[string]string{"Origin": "http://bridge.test", "Content-Type": "application/json"})
	data, _ := io.ReadAll(response.Body)
	if bytes.Contains(data, []byte("private")) {
		t.Errorf("private error leaked: %s", data)
	}
	var payload errorResponse
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error != "STM unavailable" {
		t.Errorf("public error = %q", payload.Error)
	}
}

func TestJavaScriptSelectorsMatchRenderedAnchors(t *testing.T) {
	server := newTestServer(t, testBackend(), nil)
	page := request(t, server.Handler(), http.MethodGet, "/floors/floor:v1:test", "", nil).Body.String()
	script := string(server.assets["app.js"].data)
	contracts := map[string]string{
		`[data-device-id]`:                `data-device-id=`,
		`[data-role="power-state"]`:       `data-role="power-state"`,
		`[data-role="power-command"]`:     `data-role="power-command"`,
		`[data-role="connection-status"]`: `data-role="connection-status"`,
		`[data-action]`:                   `data-action=`,
		`[data-favorite-toggle]`:          `data-favorite-toggle=`,
	}
	for selector, anchor := range contracts {
		if !strings.Contains(script, selector) {
			t.Errorf("app.js lacks selector %s", selector)
		}
		if !strings.Contains(page, anchor) {
			t.Errorf("rendered page lacks anchor %s", anchor)
		}
	}
}

func TestFavouriteScriptOnlyPrunesAgainstCompleteHomePageList(t *testing.T) {
	server := newTestServer(t, testBackend(), nil)
	script := string(server.assets["app.js"].data)
	for _, contract := range []string{
		"var valid = ids.slice();",
		"if (list) {",
		"if (list && valid.length !== ids.length)",
	} {
		if !strings.Contains(script, contract) {
			t.Errorf("favourite persistence logic lacks %q", contract)
		}
	}
	if strings.Contains(script, "CSS.escape") {
		t.Error("favourite persistence should not depend on CSS.escape browser support")
	}
}

func TestFavouriteAttributesPreserveDistinctStableDeviceIDs(t *testing.T) {
	server := newTestServer(t, testBackend(), nil)
	floorPage := request(t, server.Handler(), http.MethodGet, "/floors/floor:v1:test", "", nil).Body.String()
	for _, id := range []string{"device:v1:amd:1:2", "device:v1:emd:4:0", "device:v1:emd:7:1"} {
		if !strings.Contains(floorPage, `data-favorite-toggle="`+id+`"`) {
			t.Errorf("floor page does not preserve favourite ID %q", id)
		}
	}
	if strings.Contains(floorPage, "#ZgotmplZ") {
		t.Error("Go template URL sanitizer replaced a favourite device ID")
	}

	homePage := request(t, server.Handler(), http.MethodGet, "/", "", nil).Body.String()
	if !strings.Contains(homePage, `data-favorite-item="device:v1:amd:1:2"`) {
		t.Error("home page does not preserve favourite item IDs")
	}
	if strings.Contains(homePage, "#ZgotmplZ") {
		t.Error("Go template URL sanitizer replaced a home favourite ID")
	}
}

func TestHomeFavouritesContainServerRenderedControllableCards(t *testing.T) {
	server := newTestServer(t, testBackend(), nil)
	homePage := request(t, server.Handler(), http.MethodGet, "/", "", nil).Body.String()
	itemStart := strings.Index(homePage, `data-favorite-item="device:v1:amd:1:2"`)
	if itemStart < 0 {
		t.Fatal("home page lacks the representative favourite item")
	}
	itemEnd := strings.Index(homePage[itemStart:], "</li>")
	if itemEnd < 0 {
		t.Fatal("favourite item is not closed")
	}
	item := homePage[itemStart : itemStart+itemEnd]
	for _, anchor := range []string{
		`data-device-id="device:v1:amd:1:2"`,
		`data-favorite-toggle="device:v1:amd:1:2"`,
		`data-action="toggle"`,
		`class="favourite-location"`,
		`data-favorite-drag`,
		`data-favorite-move="up"`,
		`data-favorite-move="down"`,
	} {
		if !strings.Contains(item, anchor) {
			t.Errorf("favourite card lacks %s", anchor)
		}
	}
}

func TestFavouriteDragHandlePersistsDOMOrder(t *testing.T) {
	server := newTestServer(t, testBackend(), nil)
	script := string(server.assets["app.js"].data)
	css := string(server.assets["app.css"].data)
	for _, contract := range []string{
		`event.target.closest("[data-favorite-drag]")`,
		`handle.setPointerCapture(event.pointerId)`,
		`document.elementFromPoint(event.clientX, event.clientY)`,
		`function persistFavouriteDOMOrder(list)`,
		`window.localStorage.setItem(favouriteKey, JSON.stringify(ids));`,
	} {
		if !strings.Contains(script, contract) {
			t.Errorf("favourite drag logic lacks %q", contract)
		}
	}
	for _, rule := range []string{".favourite-drag-handle", "touch-action: none;", "cursor: grab;"} {
		if !strings.Contains(css, rule) {
			t.Errorf("favourite drag styling lacks %q", rule)
		}
	}
}

func TestFavouriteOrderControlsUpdateStoredOrder(t *testing.T) {
	server := newTestServer(t, testBackend(), nil)
	script := string(server.assets["app.js"].data)
	for _, contract := range []string{
		`document.querySelectorAll("[data-favorite-move]")`,
		`function moveFavourite(button)`,
		`ids[index] = ids[target];`,
		`ids[target] = moved;`,
		`window.localStorage.setItem(favouriteKey, JSON.stringify(ids));`,
	} {
		if !strings.Contains(script, contract) {
			t.Errorf("favourite ordering logic lacks %q", contract)
		}
	}
}
