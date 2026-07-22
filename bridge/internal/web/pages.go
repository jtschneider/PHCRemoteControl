package web

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/jtschneider/PHCRemoteControl/bridge/internal/controller"
	"github.com/jtschneider/PHCRemoteControl/bridge/internal/domain"
)

type pageData struct {
	Page          string
	Lang          string
	Title         string
	Text          translations
	Project       domain.Project
	Snapshot      controller.Snapshot
	Floors        []floorView
	Floor         *floorView
	AllDevices    []favouriteView
	STMAddress    string
	PublicOrigin  string
	ConnectionKey string
	PowerOn       bool
}

type floorView struct {
	ID          string
	Name        string
	URL         string
	DeviceCount int
	Categories  []categoryView
}

type categoryView struct {
	Name    string
	Devices []deviceView
}

type deviceView struct {
	ID                string
	Name              string
	Kind              domain.DeviceKind
	KindLabel         string
	Category          string
	Power             controller.PowerState
	PowerLabel        string
	PowerAction       string
	PowerPressed      bool
	StateCapable      bool
	SecurityAction    bool
	Actions           []actionView
	StateLabel        string
	FavouriteLabel    string
	ExperimentalLabel string
}

type actionView struct {
	Action       controller.Action
	Label        string
	Symbol       string
	Experimental bool
	Confirm      string
}

type favouriteView struct {
	Device deviceView
	Floor  string
	URL    string
}

type translations struct {
	AppName, Floors, Settings, Acknowledgments                                   string
	HomeTitle, NoFavourites, Favourites, Favourite, RemoveFavourite              string
	Reorder, MoveEarlier, MoveLater                                              string
	Devices, DeviceSingular, Back, Connection, Connected, Disconnected, Stale    string
	State, On, Off, Unknown, TurnOn, TurnOff                                     string
	Raise, Stop, Lower, TiltOpen, TiltClose, Experimental                        string
	Activate, ShortPress, LongPress, CommandSent, CommandAccepted, CommandFailed string
	Unsupported, DeviceMissing, STMUnavailable, CommandTimeout                   string
	ReloadProject, Reloading, Project, BridgeAddress, STMAddress                 string
	StorageTitle, StorageText, NoJavaScript, Language, English, German           string
	OpenSource, LicenseText, AboutText, Confirmation                             string
	Light, Outlet, Shutter, Scene, Button, Dimmer                                string
	LiveInterrupted, ReloadFailed                                                string
}

var english = translations{
	AppName: "PHC Remote", Floors: "Floors", Settings: "Settings", Acknowledgments: "Acknowledgments",
	HomeTitle: "Home", NoFavourites: "No favourites yet.", Favourites: "Favourites",
	Favourite: "Add to favourites", RemoveFavourite: "Remove from favourites",
	Reorder: "Drag to reorder", MoveEarlier: "Move earlier", MoveLater: "Move later",
	Devices: "devices", DeviceSingular: "device", Back: "All floors", Connection: "Connection",
	Connected: "STM connected", Disconnected: "STM unavailable", Stale: "State data is stale",
	State: "State", On: "On", Off: "Off", Unknown: "Unknown", TurnOn: "Turn on", TurnOff: "Turn off",
	Raise: "Raise", Stop: "Stop", Lower: "Lower", TiltOpen: "Open slats", TiltClose: "Close slats",
	Experimental: "Experimental", Activate: "Activate", ShortPress: "Short press", LongPress: "Long press",
	CommandSent: "Command sent", CommandAccepted: "Command accepted", CommandFailed: "Command failed",
	Unsupported: "Action not supported", DeviceMissing: "Device no longer exists", STMUnavailable: "STM unavailable", CommandTimeout: "STM operation timed out",
	ReloadProject: "Reload from STM", Reloading: "Reloading project", Project: "Project",
	BridgeAddress: "Website address", STMAddress: "STM address", StorageTitle: "Favourites",
	StorageText:  "Favourites are stored only in this browser. They do not sync and are lost when browser storage is cleared.",
	NoJavaScript: "Control requires JavaScript. Project navigation and current information remain available.",
	Language:     "Language", English: "English", German: "German", OpenSource: "Open-source licenses",
	LicenseText:  "This bridge uses only the Go standard library. The PHC Remote logo includes artwork derived from Mono Icons (MIT) and Material Design Icons (Apache 2.0).",
	AboutText:    "A local website for controlling a PEHA/Honeywell PHC installation.",
	Confirmation: "Run this security-sensitive action?", Light: "Light", Outlet: "Outlet", Shutter: "Shutter",
	Scene: "Scene", Button: "Button", Dimmer: "Dimmer", LiveInterrupted: "Live updates interrupted",
	ReloadFailed: "Project reload failed",
}

var german = translations{
	AppName: "PHC Remote", Floors: "Stockwerke", Settings: "Einstellungen", Acknowledgments: "Danksagungen",
	HomeTitle: "Zuhause", NoFavourites: "Noch keine Favoriten.", Favourites: "Favoriten",
	Favourite: "Zu Favoriten hinzufügen", RemoveFavourite: "Aus Favoriten entfernen",
	Reorder: "Zum Neuordnen ziehen", MoveEarlier: "Nach vorne", MoveLater: "Nach hinten",
	Devices: "Geräte", DeviceSingular: "Gerät", Back: "Alle Stockwerke", Connection: "Verbindung",
	Connected: "STM verbunden", Disconnected: "STM nicht erreichbar", Stale: "Zustandsdaten sind veraltet",
	State: "Status", On: "Ein", Off: "Aus", Unknown: "Unbekannt", TurnOn: "Einschalten", TurnOff: "Ausschalten",
	Raise: "Hoch", Stop: "Stop", Lower: "Herunter", TiltOpen: "Lamellen öffnen", TiltClose: "Lamellen schließen",
	Experimental: "Experimentell", Activate: "Aktivieren", ShortPress: "Kurzer Tastendruck", LongPress: "Langer Tastendruck",
	CommandSent: "Befehl gesendet", CommandAccepted: "Befehl angenommen", CommandFailed: "Befehl fehlgeschlagen",
	Unsupported: "Aktion nicht unterstützt", DeviceMissing: "Gerät ist nicht mehr vorhanden", STMUnavailable: "STM nicht erreichbar", CommandTimeout: "STM-Zeitüberschreitung",
	ReloadProject: "Vom STM neu laden", Reloading: "Projekt wird neu geladen", Project: "Projekt",
	BridgeAddress: "Website-Adresse", STMAddress: "STM-Adresse", StorageTitle: "Favoriten",
	StorageText:  "Favoriten werden nur in diesem Browser gespeichert. Sie werden nicht synchronisiert und gehen beim Löschen der Browserdaten verloren.",
	NoJavaScript: "Die Steuerung benötigt JavaScript. Projektnavigation und aktuelle Informationen bleiben verfügbar.",
	Language:     "Sprache", English: "Englisch", German: "Deutsch", OpenSource: "Open-Source-Lizenzen",
	LicenseText:  "Diese Bridge verwendet nur die Go-Standardbibliothek. Das PHC-Remote-Logo enthält abgeleitete Grafiken aus Mono Icons (MIT) und Material Design Icons (Apache 2.0).",
	AboutText:    "Eine lokale Website zur Steuerung einer PEHA/Honeywell-PHC-Anlage.",
	Confirmation: "Diese sicherheitsrelevante Aktion ausführen?", Light: "Licht", Outlet: "Steckdose", Shutter: "Rollladen",
	Scene: "Szene", Button: "Taster", Dimmer: "Dimmer", LiveInterrupted: "Live-Aktualisierung unterbrochen",
	ReloadFailed: "Projekt konnte nicht neu geladen werden",
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodGet) {
		return
	}
	if r.URL.Path != "/" && r.URL.Path != "/settings" && r.URL.Path != "/acknowledgments" &&
		!strings.HasPrefix(r.URL.Path, "/floors/") {
		http.NotFound(w, r)
		return
	}
	backend := s.current()
	project := backend.Project()
	snapshot := backend.Snapshot()
	lang, text := pageLanguage(r)
	data := pageData{
		Lang: lang, Text: text, Project: project, Snapshot: snapshot,
		STMAddress: s.stmAddress, PublicOrigin: s.origin.String(),
		ConnectionKey: connectionClass(snapshot),
	}
	data.Floors = s.floorViews(backend, project, snapshot, lang, text)
	data.AllDevices = allFavouriteViews(data.Floors)

	switch {
	case r.URL.Path == "/":
		data.Page, data.Title = "home", text.HomeTitle
	case r.URL.Path == "/settings":
		data.Page, data.Title = "settings", text.Settings
	case r.URL.Path == "/acknowledgments":
		data.Page, data.Title = "acknowledgments", text.Acknowledgments
	case strings.HasPrefix(r.URL.Path, "/floors/"):
		floorID, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/floors/"))
		if err != nil || floorID == "" {
			http.NotFound(w, r)
			return
		}
		for i := range data.Floors {
			if data.Floors[i].ID == floorID {
				data.Floor = &data.Floors[i]
				break
			}
		}
		if data.Floor == nil {
			http.NotFound(w, r)
			return
		}
		data.Page, data.Title = "floor", data.Floor.Name
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.templates.ExecuteTemplate(w, "base.html", data); err != nil {
		s.logger.Error("rendering page", "page", data.Page, "error", err)
	}
}

func pageLanguage(r *http.Request) (string, translations) {
	switch r.URL.Query().Get("lang") {
	case "de":
		return "de", german
	case "en":
		return "en", english
	}
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Accept-Language")), "de") {
		return "de", german
	}
	return "en", english
}

func languageQuery(lang string) string { return "?lang=" + lang }

func (s *Server) floorViews(backend Backend, project domain.Project, snapshot controller.Snapshot, lang string, text translations) []floorView {
	floors := make([]floorView, 0, len(project.Floors))
	for _, floor := range project.Floors {
		view := floorView{ID: floor.ID, Name: floor.Name, DeviceCount: len(floor.Devices),
			URL: "/floors/" + url.PathEscape(floor.ID) + languageQuery(lang)}
		byCategory := make(map[string][]deviceView)
		var categoryOrder []string
		for _, device := range floor.Devices {
			if _, exists := byCategory[device.Category]; !exists {
				categoryOrder = append(categoryOrder, device.Category)
			}
			byCategory[device.Category] = append(byCategory[device.Category], makeDeviceView(backend, device, snapshot, text))
		}
		for _, category := range categoryOrder {
			devices := byCategory[category]
			sort.SliceStable(devices, func(i, j int) bool { return strings.ToLower(devices[i].Name) < strings.ToLower(devices[j].Name) })
			view.Categories = append(view.Categories, categoryView{Name: category, Devices: devices})
		}
		floors = append(floors, view)
	}
	return floors
}

func makeDeviceView(backend Backend, device domain.Device, snapshot controller.Snapshot, text translations) deviceView {
	state, stateCapable := snapshot.Devices[device.ID]
	view := deviceView{ID: device.ID, Name: device.Name, Kind: device.Kind, Category: device.Category,
		KindLabel: kindLabel(device.Kind, text), StateCapable: stateCapable, Power: state.Power,
		SecurityAction: isSecurityAction(device), StateLabel: text.State,
		FavouriteLabel: text.Favourite, ExperimentalLabel: text.Experimental}
	view.PowerLabel, view.PowerAction, view.PowerPressed = powerPresentation(state.Power, text)
	capabilities, _ := backend.Capabilities(device.ID)
	for _, capability := range capabilities {
		if stateCapable && (capability.Action == controller.ActionOn || capability.Action == controller.ActionOff ||
			capability.Action == controller.ActionToggle) {
			continue
		}
		action := actionView{Action: capability.Action, Label: actionLabel(capability.Action, text),
			Symbol: actionSymbol(capability.Action), Experimental: capability.Experimental}
		if view.SecurityAction {
			action.Confirm = text.Confirmation
		}
		view.Actions = append(view.Actions, action)
	}
	return view
}

func allFavouriteViews(floors []floorView) []favouriteView {
	var result []favouriteView
	for _, floor := range floors {
		for _, category := range floor.Categories {
			for _, device := range category.Devices {
				result = append(result, favouriteView{Device: device, Floor: floor.Name,
					URL: floor.URL + "#device-" + url.PathEscape(device.ID)})
			}
		}
	}
	return result
}

func powerPresentation(power controller.PowerState, text translations) (label, action string, pressed bool) {
	switch power {
	case controller.PowerOn:
		return text.On, text.TurnOff, true
	case controller.PowerOff:
		return text.Off, text.TurnOn, false
	default:
		return text.Unknown, text.TurnOn, false
	}
}

func kindLabel(kind domain.DeviceKind, text translations) string {
	switch kind {
	case domain.KindLight:
		return text.Light
	case domain.KindOutlet:
		return text.Outlet
	case domain.KindShutter:
		return text.Shutter
	case domain.KindScene:
		return text.Scene
	case domain.KindButton:
		return text.Button
	case domain.KindDimmer:
		return text.Dimmer
	default:
		return string(kind)
	}
}

func actionLabel(action controller.Action, text translations) string {
	switch action {
	case controller.ActionOn:
		return text.TurnOn
	case controller.ActionOff:
		return text.TurnOff
	case controller.ActionRaise:
		return text.Raise
	case controller.ActionStop:
		return text.Stop
	case controller.ActionLower:
		return text.Lower
	case controller.ActionTiltOpen:
		return text.TiltOpen
	case controller.ActionTiltClose:
		return text.TiltClose
	case controller.ActionShortPress:
		return text.ShortPress
	case controller.ActionLongPress:
		return text.LongPress
	case controller.ActionActivate:
		return text.Activate
	default:
		return string(action)
	}
}

func actionSymbol(action controller.Action) string {
	switch action {
	case controller.ActionRaise:
		return "up"
	case controller.ActionLower:
		return "down"
	case controller.ActionStop:
		return "stop"
	case controller.ActionTiltOpen:
		return "tilt-open"
	case controller.ActionTiltClose:
		return "tilt-close"
	default:
		return ""
	}
}

func isSecurityAction(device domain.Device) bool {
	haystack := strings.ToLower(device.Category + " " + device.Name)
	for _, word := range []string{"panic", "panik", "security", "sicherheit", "alarm"} {
		if strings.Contains(haystack, word) {
			return true
		}
	}
	return false
}

func connectionClass(snapshot controller.Snapshot) string {
	if snapshot.Connection != controller.ConnectionConnected {
		return "disconnected"
	}
	if snapshot.Stale {
		return "stale"
	}
	return "connected"
}

func deviceCountLabel(count int, text translations) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", text.DeviceSingular)
	}
	return fmt.Sprintf("%d %s", count, text.Devices)
}
