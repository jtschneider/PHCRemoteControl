package project

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jtschneider/PHCRemoteControl/bridge/internal/domain"
)

const (
	MaxProjectXMLBytes  = 8 << 20
	MaxVisibleChannels  = 4096
	MaxChannelTextRunes = 512
	MaxToolActions      = 256
	MaxToolCandidates   = 4096
	MaxLocationDepth    = 64
)

type rawChannel struct {
	moduleGroup  string
	moduleName   string
	moduleAddr   int
	channelGroup string
	channelAddr  int
	text         string
}

type channelParts struct {
	sortIndex int
	floor     string
	category  string
	label     string
}

// Parse parses the hardware/UI project and the supported subset of optional
// automation tools. Passing nil for tpfxData means the archive had no TPFX.
func Parse(ppfxData, tpfxData []byte) (domain.Project, error) {
	name, channels, err := collectPPFX(ppfxData)
	if err != nil {
		return domain.Project{}, err
	}

	var actions []toolAction
	if tpfxData != nil {
		actions, err = collectToolActions(tpfxData)
		if err != nil {
			return domain.Project{}, err
		}
	}
	return buildProject(name, channels, actions)
}

// ParsePPFX parses a project without optional TPFX actions.
func ParsePPFX(data []byte) (domain.Project, error) {
	return Parse(data, nil)
}

func collectPPFX(data []byte) (string, []rawChannel, error) {
	if len(data) == 0 {
		return "", nil, fmt.Errorf("project: project.ppfx is empty")
	}
	if len(data) > MaxProjectXMLBytes {
		return "", nil, fmt.Errorf("project: project.ppfx exceeds %d bytes", MaxProjectXMLBytes)
	}

	dec := xml.NewDecoder(bytes.NewReader(data))
	projectName := "PHC"
	var channels []rawChannel

	var moduleGroups []string
	currentModuleGroup := ""
	currentModuleName := ""
	currentModuleAddr := -1
	currentChannelGroup := ""

	for {
		token, err := dec.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", nil, fmt.Errorf("project: parsing project.ppfx: %w", err)
		}

		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "PROJECT":
				if name := attribute(element.Attr, "name"); name != "" {
					projectName = name
				}
			case "MODS":
				moduleGroups = append(moduleGroups, currentModuleGroup)
				currentModuleGroup = attribute(element.Attr, "grp")
			case "MOD":
				currentModuleName = attribute(element.Attr, "name")
				currentModuleAddr = parseIntOr(attribute(element.Attr, "adr"), -1)
			case "CHAS":
				currentChannelGroup = attribute(element.Attr, "grp")
			case "CHA":
				var text string
				if err := dec.DecodeElement(&text, &element); err != nil {
					return "", nil, fmt.Errorf("project: parsing channel: %w", err)
				}
				if attribute(element.Attr, "visu") != "true" {
					continue
				}
				if utf8.RuneCountInString(text) > MaxChannelTextRunes {
					return "", nil, fmt.Errorf("project: channel label exceeds %d characters", MaxChannelTextRunes)
				}
				text = strings.TrimSpace(text)
				if text == "" {
					continue
				}
				if len(channels) >= MaxVisibleChannels {
					return "", nil, fmt.Errorf("project: more than %d visible channels", MaxVisibleChannels)
				}
				channels = append(channels, rawChannel{
					moduleGroup:  currentModuleGroup,
					moduleName:   currentModuleName,
					moduleAddr:   currentModuleAddr,
					channelGroup: currentChannelGroup,
					channelAddr:  parseIntOr(attribute(element.Attr, "adr"), -1),
					text:         text,
				})
			}
		case xml.EndElement:
			switch element.Name.Local {
			case "MODS":
				if n := len(moduleGroups); n > 0 {
					currentModuleGroup = moduleGroups[n-1]
					moduleGroups = moduleGroups[:n-1]
				} else {
					currentModuleGroup = ""
				}
			case "MOD":
				currentModuleName = ""
				currentModuleAddr = -1
			case "CHAS":
				currentChannelGroup = ""
			}
		}
	}

	return projectName, channels, nil
}

type floorBuilder struct {
	sortIndex int
	devices   []domain.Device
}

type projectBuilder struct {
	floors  map[string]*floorBuilder
	seenIDs map[string]struct{}
}

func newProjectBuilder() *projectBuilder {
	return &projectBuilder{
		floors:  make(map[string]*floorBuilder),
		seenIDs: make(map[string]struct{}),
	}
}

func (b *projectBuilder) add(parts channelParts, device domain.Device) error {
	if device.Ref.DIP < 0 || device.Ref.Channel < 0 {
		return fmt.Errorf("project: invalid %s address for %q", device.Ref.ModuleClass, device.Name)
	}
	if device.ID == "" {
		device.ID = domain.StableDeviceID(device.Ref)
	}
	if _, exists := b.seenIDs[device.ID]; exists {
		return fmt.Errorf("project: duplicate device identity %q", device.ID)
	}
	b.seenIDs[device.ID] = struct{}{}

	floor := b.floors[parts.floor]
	if floor == nil {
		floor = &floorBuilder{}
		b.floors[parts.floor] = floor
	}
	floor.sortIndex = parts.sortIndex
	floor.devices = append(floor.devices, device)
	return nil
}

type motorKey struct {
	sortIndex int
	floor     string
	category  string
	label     string
}

type motorChannel struct {
	module  int
	channel int
}

func buildProject(name string, channels []rawChannel, actions []toolAction) (domain.Project, error) {
	builder := newProjectBuilder()

	// Visible AMD outputs become pollable lights/outlets. Motor-like AMD labels
	// are skipped because the controllable motor inputs are represented below.
	for _, channel := range channels {
		if channel.moduleGroup != "Ausgangsmodule" ||
			channel.channelGroup != "Ausgang" ||
			!strings.HasPrefix(channel.moduleName, "AMD") {
			continue
		}
		parts, ok := parseChannelParts(channel.text)
		if !ok {
			continue
		}
		kind := classifyOutput(parts.category)
		if kind == domain.KindShutter {
			continue
		}
		ref := domain.ChannelRef{
			ModuleClass: domain.ModuleAMD,
			DIP:         channel.moduleAddr,
			Channel:     channel.channelAddr,
		}
		if err := builder.add(parts, domain.Device{
			Name:     parts.label,
			Kind:     kind,
			Category: parts.category,
			Ref:      ref,
		}); err != nil {
			return domain.Project{}, err
		}
	}

	// Pair visible motor-like EMD inputs only when both direction channels are
	// present. Unpaired or unrecognised inputs remain available as buttons later.
	downChannels := make(map[motorKey]motorChannel)
	upChannels := make(map[motorKey]motorChannel)
	representedInputs := make(map[domain.ChannelRef]struct{})
	for _, channel := range channels {
		if channel.moduleGroup != "Eingangsmodule" || channel.channelGroup != "Eingang" {
			continue
		}
		parts, ok := parseChannelParts(channel.text)
		if !ok || classifyOutput(parts.category) != domain.KindShutter {
			continue
		}
		direction, ok := motorDirection(parts.label)
		if !ok {
			continue
		}
		baseLabel := stripMotorDirectionSuffix(parts.label)
		if baseLabel == "" {
			continue
		}
		key := motorKey{
			sortIndex: parts.sortIndex,
			floor:     parts.floor,
			category:  parts.category,
			label:     baseLabel,
		}
		value := motorChannel{module: channel.moduleAddr, channel: channel.channelAddr}
		if direction == directionDown {
			downChannels[key] = value
		} else {
			upChannels[key] = value
		}
	}

	for key, down := range downChannels {
		up, paired := upChannels[key]
		if !paired {
			continue
		}
		downRef := domain.ChannelRef{ModuleClass: domain.ModuleEMD, DIP: down.module, Channel: down.channel}
		upRef := domain.ChannelRef{ModuleClass: domain.ModuleEMD, DIP: up.module, Channel: up.channel}
		parts := channelParts{sortIndex: key.sortIndex, floor: key.floor, category: key.category, label: key.label}
		if err := builder.add(parts, domain.Device{
			Name:     key.label,
			Kind:     domain.KindShutter,
			Category: key.category,
			Ref:      downRef,
			UpRef:    &upRef,
		}); err != nil {
			return domain.Project{}, err
		}
		representedInputs[downRef] = struct{}{}
		representedInputs[upRef] = struct{}{}
	}

	// EMD_VIR channels are momentary central actions unless their category is
	// motor-like. Motor-like virtual inputs fall through to the generic fallback.
	for _, channel := range channels {
		if channel.moduleName != "EMD_VIR" || channel.channelGroup != "Eingang" {
			continue
		}
		parts, ok := parseChannelParts(channel.text)
		if !ok || classifyOutput(parts.category) == domain.KindShutter {
			continue
		}
		ref := domain.ChannelRef{ModuleClass: domain.ModuleEMD, DIP: channel.moduleAddr, Channel: channel.channelAddr}
		if err := builder.add(parts, domain.Device{
			Name:     parts.label,
			Kind:     domain.KindScene,
			Category: parts.category,
			Ref:      ref,
		}); err != nil {
			return domain.Project{}, err
		}
		representedInputs[ref] = struct{}{}
	}

	// Every remaining visible EMD input is deliberately exposed without guessing
	// its wiring. The controller can later offer short- and long-press actions.
	for _, channel := range channels {
		if channel.moduleGroup != "Eingangsmodule" || channel.channelGroup != "Eingang" {
			continue
		}
		parts, ok := parseChannelParts(channel.text)
		if !ok {
			continue
		}
		ref := domain.ChannelRef{ModuleClass: domain.ModuleEMD, DIP: channel.moduleAddr, Channel: channel.channelAddr}
		if _, represented := representedInputs[ref]; represented {
			continue
		}
		if err := builder.add(parts, domain.Device{
			Name:     parts.label,
			Kind:     domain.KindButton,
			Category: parts.category,
			Ref:      ref,
		}); err != nil {
			return domain.Project{}, err
		}
		representedInputs[ref] = struct{}{}
	}

	// TPFX tools are a last-resort source for selected actions. Do not duplicate
	// an action already represented by a PPFX scene or fallback button.
	for _, action := range actions {
		if _, represented := representedInputs[action.ref]; represented {
			continue
		}
		parts := channelParts{
			sortIndex: action.sortIndex,
			floor:     action.floor,
			category:  action.category,
			label:     action.name,
		}
		if err := builder.add(parts, domain.Device{
			Name:     action.name,
			Kind:     domain.KindScene,
			Category: action.category,
			Ref:      action.ref,
		}); err != nil {
			return domain.Project{}, err
		}
	}

	project := domain.Project{Name: name}
	for floorName, floor := range builder.floors {
		sort.SliceStable(floor.devices, func(i, j int) bool {
			left, right := floor.devices[i], floor.devices[j]
			if rank := kindRank(left.Kind) - kindRank(right.Kind); rank != 0 {
				return rank < 0
			}
			if left.Category != right.Category {
				return naturalLess(left.Category, right.Category)
			}
			return naturalLess(left.Name, right.Name)
		})
		project.Floors = append(project.Floors, domain.Floor{
			ID:        domain.StableFloorID(floorName),
			Name:      floorName,
			Devices:   floor.devices,
			SortIndex: floor.sortIndex,
		})
	}
	sort.SliceStable(project.Floors, func(i, j int) bool {
		if project.Floors[i].SortIndex != project.Floors[j].SortIndex {
			return project.Floors[i].SortIndex < project.Floors[j].SortIndex
		}
		return naturalLess(project.Floors[i].Name, project.Floors[j].Name)
	})
	return project, nil
}

func parseChannelParts(text string) (channelParts, bool) {
	colon := strings.IndexByte(text, ':')
	if colon < 0 {
		return channelParts{}, false
	}
	relativeArrow := strings.IndexByte(text[colon+1:], '>')
	if relativeArrow < 0 {
		return channelParts{}, false
	}
	arrow := colon + 1 + relativeArrow

	prefix := strings.TrimSpace(text[:colon])
	category := strings.TrimSpace(text[colon+1 : arrow])
	label := strings.TrimSpace(text[arrow+1:])
	if category == "" || label == "" {
		return channelParts{}, false
	}

	dotParts := strings.Split(prefix, ".")
	sortIndex := 99
	if len(dotParts) > 0 {
		sortIndex = parseIntOr(dotParts[0], 99)
	}
	floor := strings.TrimSpace(strings.Join(dotParts[1:], "."))
	if floor == "" {
		return channelParts{}, false
	}
	return channelParts{sortIndex: sortIndex, floor: floor, category: category, label: label}, true
}

func classifyOutput(category string) domain.DeviceKind {
	if matchesKeyword(category, shutterKeywords) || matchesKeyword(category, motorizedWindowKeywords) {
		return domain.KindShutter
	}
	if matchesKeyword(category, outletKeywords) {
		return domain.KindOutlet
	}
	return domain.KindLight
}

type motorDirectionValue uint8

const (
	directionUp motorDirectionValue = iota
	directionDown
)

var downDirectionWords = map[string]struct{}{
	"senken": {}, "lower": {}, "down": {}, "zu": {}, "close": {},
	"closing": {}, "schliessen": {}, "schließen": {},
}

var upDirectionWords = map[string]struct{}{
	"heben": {}, "raise": {}, "up": {}, "auf": {}, "open": {},
	"opening": {}, "offnen": {}, "oeffnen": {}, "öffnen": {},
}

func motorDirection(label string) (motorDirectionValue, bool) {
	words := strings.Fields(label)
	if len(words) == 0 {
		return 0, false
	}
	last := normalizeDirectionWord(words[len(words)-1])
	if _, ok := downDirectionWords[last]; ok {
		return directionDown, true
	}
	if _, ok := upDirectionWords[last]; ok {
		return directionUp, true
	}
	return 0, false
}

func stripMotorDirectionSuffix(label string) string {
	words := strings.Fields(label)
	if len(words) == 0 {
		return strings.TrimSpace(label)
	}
	last := normalizeDirectionWord(words[len(words)-1])
	if _, ok := downDirectionWords[last]; ok {
		return strings.Join(words[:len(words)-1], " ")
	}
	if _, ok := upDirectionWords[last]; ok {
		return strings.Join(words[:len(words)-1], " ")
	}
	return strings.TrimSpace(label)
}

func normalizeDirectionWord(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// naturalLess supplies the numeric ordering used by Swift's
// localizedStandardCompare for ordinary project labels ("Light 2" before
// "Light 10") without making ordering depend on the Pi's locale.
func naturalLess(left, right string) bool {
	l, r := []rune(strings.ToLower(left)), []rune(strings.ToLower(right))
	for li, ri := 0, 0; li < len(l) && ri < len(r); {
		if isASCIIDigit(l[li]) && isASCIIDigit(r[ri]) {
			lend, rend := li, ri
			for lend < len(l) && isASCIIDigit(l[lend]) {
				lend++
			}
			for rend < len(r) && isASCIIDigit(r[rend]) {
				rend++
			}
			leftDigits := strings.TrimLeft(string(l[li:lend]), "0")
			rightDigits := strings.TrimLeft(string(r[ri:rend]), "0")
			if leftDigits == "" {
				leftDigits = "0"
			}
			if rightDigits == "" {
				rightDigits = "0"
			}
			if len(leftDigits) != len(rightDigits) {
				return len(leftDigits) < len(rightDigits)
			}
			if leftDigits != rightDigits {
				return leftDigits < rightDigits
			}
			if lend-li != rend-ri {
				return lend-li < rend-ri
			}
			li, ri = lend, rend
			continue
		}
		if l[li] != r[ri] {
			return l[li] < r[ri]
		}
		li++
		ri++
		if li == len(l) || ri == len(r) {
			return li == len(l) && ri != len(r)
		}
	}
	return len(l) < len(r)
}

func isASCIIDigit(value rune) bool {
	return value >= '0' && value <= '9'
}

func kindRank(kind domain.DeviceKind) int {
	switch kind {
	case domain.KindLight:
		return 0
	case domain.KindDimmer:
		return 1
	case domain.KindShutter:
		return 2
	case domain.KindOutlet:
		return 3
	case domain.KindScene:
		return 4
	case domain.KindButton:
		return 5
	default:
		return 99
	}
}

func attribute(attributes []xml.Attr, name string) string {
	for _, attribute := range attributes {
		if attribute.Name.Local == name {
			return attribute.Value
		}
	}
	return ""
}

func parseIntOr(value string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return parsed
}
