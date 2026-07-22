// Package domain contains the transport-independent PHC project model shared by
// the parser, controller, and web layers.
package domain

import (
	"encoding/base64"
	"fmt"
)

type ModuleClass string

const (
	ModuleEMD ModuleClass = "emd"
	ModuleAMD ModuleClass = "amd"
	ModuleJRM ModuleClass = "jrm"
)

type DeviceKind string

const (
	KindLight   DeviceKind = "light"
	KindDimmer  DeviceKind = "dimmer"
	KindOutlet  DeviceKind = "outlet"
	KindShutter DeviceKind = "shutter"
	KindScene   DeviceKind = "scene"
	KindButton  DeviceKind = "button"
)

type ChannelRef struct {
	ModuleClass ModuleClass `json:"moduleClass"`
	DIP         int         `json:"dip"`
	Channel     int         `json:"channel"`
}

func StableDeviceID(ref ChannelRef) string {
	return fmt.Sprintf("device:v1:%s:%d:%d", ref.ModuleClass, ref.DIP, ref.Channel)
}

func StableFloorID(name string) string {
	return "floor:v1:" + base64.RawURLEncoding.EncodeToString([]byte(name))
}

type Device struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	Kind     DeviceKind  `json:"kind"`
	Category string      `json:"category"`
	Ref      ChannelRef  `json:"ref"`
	UpRef    *ChannelRef `json:"upRef,omitempty"`
}

type Floor struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Devices   []Device `json:"devices"`
	SortIndex int      `json:"-"`
}

type Project struct {
	Name   string  `json:"name"`
	Floors []Floor `json:"floors"`
}
