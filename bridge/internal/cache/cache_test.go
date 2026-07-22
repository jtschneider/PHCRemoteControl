package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jtschneider/PHCRemoteControl/bridge/internal/domain"
)

func sampleProject() domain.Project {
	return domain.Project{
		Name: "Haus",
		Floors: []domain.Floor{{
			ID:   "floor:v1:EG",
			Name: "EG",
			Devices: []domain.Device{{
				ID: "device:v1:amd:3:10", Name: "Küche", Kind: domain.KindLight,
				Category: "Licht", Ref: domain.ChannelRef{ModuleClass: domain.ModuleAMD, DIP: 3, Channel: 10},
			}},
		}},
	}
}

func TestStore_SaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := New(dir, "192.168.1.50:6680")
	want := sampleProject()

	if err := store.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// File must be private.
	info, err := os.Stat(filepath.Join(dir, "project.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("cache mode = %o, want 600", perm)
	}

	got, ok, err := store.Load()
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if got.Name != want.Name || len(got.Floors) != 1 || got.Floors[0].Devices[0].ID != want.Floors[0].Devices[0].ID {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestStore_DifferentSTMIgnored(t *testing.T) {
	dir := t.TempDir()
	New(dir, "192.168.1.50:6680").Save(sampleProject())

	_, ok, err := New(dir, "10.0.0.9:6680").Load() // different STM key
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("a cache keyed to a different STM must not load")
	}
}

func TestStore_SchemaMismatchIgnored(t *testing.T) {
	dir := t.TempDir()
	blob, _ := json.Marshal(cacheFile{SchemaVersion: 999, STMKey: "x", Project: sampleProject()})
	os.WriteFile(filepath.Join(dir, "project.json"), blob, 0o600)

	_, ok, _ := New(dir, "anything").Load()
	if ok {
		t.Error("an incompatible schema must not load")
	}
}

func TestStore_CorruptIgnored(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "project.json"), []byte("{not json"), 0o600)

	_, ok, err := New(dir, "x").Load()
	if err != nil || ok {
		t.Errorf("corrupt cache should be ignored, got ok=%v err=%v", ok, err)
	}
}

func TestStore_MissingReturnsNotFound(t *testing.T) {
	_, ok, err := New(t.TempDir(), "x").Load()
	if err != nil || ok {
		t.Errorf("missing cache: ok=%v err=%v", ok, err)
	}
}

func TestStore_NilIsNoOp(t *testing.T) {
	var store *Store // caching disabled
	if err := store.Save(sampleProject()); err != nil {
		t.Errorf("nil Save: %v", err)
	}
	if _, ok, err := store.Load(); ok || err != nil {
		t.Errorf("nil Load: ok=%v err=%v", ok, err)
	}
}
