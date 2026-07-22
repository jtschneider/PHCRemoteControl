package project

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/jtschneider/PHCRemoteControl/bridge/internal/domain"
)

type toolAction struct {
	name      string
	category  string
	floor     string
	sortIndex int
	ref       domain.ChannelRef
}

type actionKind uint8

const (
	actionPanic actionKind = iota
	actionPresence
)

type toolCandidate struct {
	index        int
	ref          domain.ChannelRef
	isPushButton bool
}

func collectToolActions(data []byte) ([]toolAction, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("project: project.tpfx is empty")
	}
	if len(data) > MaxProjectXMLBytes {
		return nil, fmt.Errorf("project: project.tpfx exceeds %d bytes", MaxProjectXMLBytes)
	}

	decoder := xml.NewDecoder(bytes.NewReader(data))
	var locations []string
	var locationElements []bool
	var tool map[string]string
	var node map[string]string
	var candidates []toolCandidate
	var actions []toolAction

	for {
		token, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("project: parsing project.tpfx: %w", err)
		}

		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "PAGE", "LAYER":
				name := attribute(element.Attr, "name")
				pushed := name != ""
				if pushed {
					if len(locations) >= MaxLocationDepth {
						return nil, fmt.Errorf("project: project.tpfx nesting exceeds %d levels", MaxLocationDepth)
					}
					locations = append(locations, name)
				}
				locationElements = append(locationElements, pushed)
			case "TOOL":
				tool = attributesMap(element.Attr)
				candidates = candidates[:0]
			case "NODE":
				node = attributesMap(element.Attr)
			case "VAR":
				if _, supported := toolActionKind(tool); !supported ||
					node == nil || node["ntype"] != "ntInput" ||
					attribute(element.Attr, "modGrp") != "Eingangsmodule" ||
					attribute(element.Attr, "chGrp") != "Eingang" {
					continue
				}
				module, moduleErr := strconv.Atoi(attribute(element.Attr, "mod"))
				channel, channelErr := strconv.Atoi(attribute(element.Attr, "cha"))
				if moduleErr != nil || channelErr != nil || module < 0 || channel < 0 {
					continue
				}
				if len(candidates) >= MaxToolCandidates {
					return nil, fmt.Errorf("project: project.tpfx contains too many tool input candidates")
				}
				objects := strings.ToLower(node["comfortSoftwareObjects"])
				name := strings.ToLower(node["name"])
				candidates = append(candidates, toolCandidate{
					index: parseIntOr(node["index"], 99),
					ref: domain.ChannelRef{
						ModuleClass: domain.ModuleEMD,
						DIP:         module,
						Channel:     channel,
					},
					isPushButton: strings.Contains(objects, "pushbutton") || strings.Contains(name, "taster"),
				})
			}
		case xml.EndElement:
			switch element.Name.Local {
			case "TOOL":
				kind, supported := toolActionKind(tool)
				if supported && len(candidates) > 0 {
					if len(actions) >= MaxToolActions {
						return nil, fmt.Errorf("project: project.tpfx contains more than %d supported actions", MaxToolActions)
					}
					candidate := preferredToolCandidate(candidates)
					floor := defaultActionFloor(kind)
					if len(locations) > 0 {
						floor = locations[len(locations)-1]
					}
					rawName := firstNonEmpty(tool["bez"], tool["name"], defaultActionName(kind))
					actions = append(actions, toolAction{
						name:      cleanActionName(rawName, kind),
						category:  actionCategory(kind),
						floor:     floor,
						sortIndex: actionSortIndex(kind),
						ref:       candidate.ref,
					})
				}
				tool = nil
				candidates = candidates[:0]
			case "NODE":
				node = nil
			case "PAGE", "LAYER":
				if n := len(locationElements); n > 0 {
					if locationElements[n-1] && len(locations) > 0 {
						locations = locations[:len(locations)-1]
					}
					locationElements = locationElements[:n-1]
				}
			}
		}
	}

	return actions, nil
}

func toolActionKind(attributes map[string]string) (actionKind, bool) {
	if attributes == nil || attributes["enable"] == "false" {
		return 0, false
	}
	haystack := strings.Join([]string{
		attributes["internalName"],
		attributes["name"],
		attributes["bez"],
		attributes["grp"],
		attributes["comfortSoftwareGroupType"],
	}, " ")
	if matchesKeyword(haystack, panicKeywords) {
		return actionPanic, true
	}
	if matchesKeyword(haystack, presenceSimulationKeywords) ||
		strings.Contains(strings.ToLower(haystack), "infratec.tools.anwesenheitssimulation") {
		return actionPresence, true
	}
	return 0, false
}

func preferredToolCandidate(candidates []toolCandidate) toolCandidate {
	ordered := append([]toolCandidate(nil), candidates...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].isPushButton != ordered[j].isPushButton {
			return ordered[i].isPushButton
		}
		return ordered[i].index < ordered[j].index
	})
	return ordered[0]
}

func actionCategory(kind actionKind) string {
	if kind == actionPanic {
		return "Security"
	}
	return "Presence Simulation"
}

func defaultActionFloor(kind actionKind) string {
	if kind == actionPanic {
		return "Security"
	}
	return "Automation"
}

func defaultActionName(kind actionKind) string {
	if kind == actionPanic {
		return "Panic Button"
	}
	return "Presence Simulation"
}

func actionSortIndex(kind actionKind) int {
	if kind == actionPanic {
		return 0
	}
	return 90
}

func cleanActionName(name string, kind actionKind) string {
	name = strings.TrimSpace(name)
	if kind == actionPresence {
		for _, prefix := range []string{"Anwesenheitssimulation ", "Presence Simulation "} {
			if strings.HasPrefix(name, prefix) {
				return strings.TrimPrefix(name, prefix)
			}
		}
	}
	return name
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func attributesMap(attributes []xml.Attr) map[string]string {
	result := make(map[string]string, len(attributes))
	for _, attribute := range attributes {
		result[attribute.Name.Local] = attribute.Value
	}
	return result
}
