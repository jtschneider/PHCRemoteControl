package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseSharedFixtures(t *testing.T) {
	tests := []struct {
		name string
		tpfx string
	}{
		{name: "amd-basic"},
		{name: "full-project", tpfx: "full-project.tpfx"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ppfx := readSharedFixture(t, test.name+".ppfx")
			var tpfx []byte
			if test.tpfx != "" {
				tpfx = readSharedFixture(t, test.tpfx)
			}
			expectedData := readSharedFixture(t, test.name+".expected.json")

			got, err := Parse(ppfx, tpfx)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}

			var expectedEnvelope struct {
				SchemaVersion int             `json:"schemaVersion"`
				Project       json.RawMessage `json:"project"`
			}
			if err := json.Unmarshal(expectedData, &expectedEnvelope); err != nil {
				t.Fatalf("decode expected fixture: %v", err)
			}
			if expectedEnvelope.SchemaVersion != 1 {
				t.Fatalf("fixture schema version = %d, want 1", expectedEnvelope.SchemaVersion)
			}

			gotJSON, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("marshal parsed project: %v", err)
			}
			assertJSONEqual(t, gotJSON, expectedEnvelope.Project)
		})
	}
}

func TestParsePPFX_RejectsMalformedAndOversizeInput(t *testing.T) {
	if _, err := ParsePPFX(nil); err == nil {
		t.Error("empty input should fail")
	}
	if _, err := ParsePPFX([]byte("<PROJECT>")); err == nil {
		t.Error("malformed XML should fail")
	}
	if _, err := ParsePPFX([]byte(strings.Repeat("x", MaxProjectXMLBytes+1))); err == nil {
		t.Error("oversize XML should fail")
	}
}

func TestParseRejectsMalformedAndOversizeTPFX(t *testing.T) {
	ppfx := []byte(`<PROJECT/>`)
	for name, tpfx := range map[string][]byte{
		"empty":     {},
		"malformed": []byte(`<PROJECT>`),
		"oversize":  []byte(strings.Repeat("x", MaxProjectXMLBytes+1)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(ppfx, tpfx); err == nil {
				t.Fatal("invalid TPFX should fail")
			}
		})
	}
}

func TestParsePPFXLimits(t *testing.T) {
	t.Run("channel label", func(t *testing.T) {
		label := strings.Repeat("x", MaxChannelTextRunes+1)
		input := fmt.Sprintf(`<PROJECT><MODS grp="Ausgangsmodule"><MOD adr="0" name="AMD"><CHAS grp="Ausgang"><CHA adr="0" visu="true">%s</CHA></CHAS></MOD></MODS></PROJECT>`, label)
		if _, err := ParsePPFX([]byte(input)); err == nil {
			t.Fatal("oversize channel label should fail")
		}
	})

	t.Run("visible channels", func(t *testing.T) {
		var input strings.Builder
		input.WriteString(`<PROJECT><MODS grp="Ausgangsmodule"><MOD adr="0" name="AMD"><CHAS grp="Ausgang">`)
		for i := 0; i <= MaxVisibleChannels; i++ {
			fmt.Fprintf(&input, `<CHA adr="%d" visu="true">1.EG : Licht &gt; L%d</CHA>`, i, i)
		}
		input.WriteString(`</CHAS></MOD></MODS></PROJECT>`)
		if _, err := ParsePPFX([]byte(input.String())); err == nil {
			t.Fatal("too many visible channels should fail")
		}
	})
}

func TestParseTPFXLimits(t *testing.T) {
	ppfx := []byte(`<PROJECT/>`)

	t.Run("location depth", func(t *testing.T) {
		input := `<PROJECT>` + strings.Repeat(`<LAYER name="x">`, MaxLocationDepth+1) +
			strings.Repeat(`</LAYER>`, MaxLocationDepth+1) + `</PROJECT>`
		if _, err := Parse(ppfx, []byte(input)); err == nil {
			t.Fatal("excessive location depth should fail")
		}
	})

	t.Run("tool candidates", func(t *testing.T) {
		var input strings.Builder
		input.WriteString(`<PROJECT><TOOL enable="true" internalName="panic"><NODE ntype="ntInput">`)
		for i := 0; i <= MaxToolCandidates; i++ {
			fmt.Fprintf(&input, `<VAR modGrp="Eingangsmodule" chGrp="Eingang" mod="0" cha="%d"/>`, i)
		}
		input.WriteString(`</NODE></TOOL></PROJECT>`)
		if _, err := Parse(ppfx, []byte(input.String())); err == nil {
			t.Fatal("too many candidates should fail")
		}
	})

	t.Run("tool actions", func(t *testing.T) {
		var input strings.Builder
		input.WriteString(`<PROJECT>`)
		for i := 0; i <= MaxToolActions; i++ {
			fmt.Fprintf(&input, `<TOOL enable="true" internalName="panic"><NODE ntype="ntInput"><VAR modGrp="Eingangsmodule" chGrp="Eingang" mod="0" cha="%d"/></NODE></TOOL>`, i)
		}
		input.WriteString(`</PROJECT>`)
		if _, err := Parse(ppfx, []byte(input.String())); err == nil {
			t.Fatal("too many actions should fail")
		}
	})
}

func readSharedFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "..", "protocol-fixtures", "project", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read shared fixture %s: %v", name, err)
	}
	return data
}

func assertJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode actual JSON: %v", err)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("decode expected JSON: %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("normalized project mismatch\n got: %s\nwant: %s", got, want)
	}
}
