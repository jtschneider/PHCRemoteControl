package domain

import "testing"

func TestStableIdentities(t *testing.T) {
	ref := ChannelRef{ModuleClass: ModuleAMD, DIP: 2, Channel: 7}
	if got, want := StableDeviceID(ref), "device:v1:amd:2:7"; got != want {
		t.Fatalf("StableDeviceID = %q, want %q", got, want)
	}
	if got, want := StableFloorID("EG"), "floor:v1:RUc"; got != want {
		t.Fatalf("StableFloorID = %q, want %q", got, want)
	}
}
