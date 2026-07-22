package domain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestInputEventPlansSharedFixture(t *testing.T) {
	path := filepath.Join("..", "..", "..", "protocol-fixtures", "commands", "input-events.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read shared fixture: %v", err)
	}
	var fixture struct {
		SchemaVersion int `json:"schemaVersion"`
		Plans         struct {
			ShortPress []InputEvent `json:"shortPress"`
			LongPress  []InputEvent `json:"longPress"`
			Tip        []InputEvent `json:"tip"`
		} `json:"plans"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode shared fixture: %v", err)
	}
	if fixture.SchemaVersion != 1 {
		t.Fatalf("schema version = %d, want 1", fixture.SchemaVersion)
	}

	cases := []struct {
		name string
		got  []InputEvent
		want []InputEvent
	}{
		{"shortPress", ShortPressEvents(), fixture.Plans.ShortPress},
		{"longPress", LongPressEvents(), fixture.Plans.LongPress},
		{"tip", TipEvents(), fixture.Plans.Tip},
	}
	for _, test := range cases {
		if !reflect.DeepEqual(test.got, test.want) {
			t.Errorf("%s events = %v, want %v", test.name, test.got, test.want)
		}
	}
}
