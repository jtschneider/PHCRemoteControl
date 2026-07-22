package stm

import (
	"errors"
	"testing"
)

func TestDecodeResponse_WhoAreYouStruct(t *testing.T) {
	data := []byte(`<?xml version="1.0"?><methodResponse><params><param><value><struct>` +
		`<member><name>STM-Address</name><value><i4>0</i4></value></member>` +
		`<member><name>Facility-ID</name><value><string>abc123</string></value></member>` +
		`<member><name>Device-Name</name><value><string>Steuermodul 0</string></value></member>` +
		`</struct></value></param></params></methodResponse>`)

	v, err := decodeResponse(data)
	if err != nil {
		t.Fatalf("decodeResponse: %v", err)
	}
	s, ok := v.(structValue)
	if !ok {
		t.Fatalf("expected structValue, got %T", v)
	}
	if n, _ := s.int("STM-Address"); n != 0 {
		t.Errorf("STM-Address = %d, want 0", n)
	}
	if got, _ := s.string("Device-Name"); got != "Steuermodul 0" {
		t.Errorf("Device-Name = %q", got)
	}
}

func TestDecodeResponse_Fault(t *testing.T) {
	data := []byte(`<methodResponse><fault><value><struct>` +
		`<member><name>faultCode</name><value><i4>4</i4></value></member>` +
		`<member><name>faultString</name><value><string>nope</string></value></member>` +
		`</struct></value></fault></methodResponse>`)

	_, err := decodeResponse(data)
	var f Fault
	if !errors.As(err, &f) {
		t.Fatalf("expected Fault, got %v", err)
	}
	if f.Code != 4 || f.Message != "nope" {
		t.Errorf("fault = %+v", f)
	}
}

func TestDecodeResponse_IntArray(t *testing.T) {
	// Shape of a sendTelegram reply: [0, addr, toggle, ?, bitmask].
	data := []byte(`<methodResponse><params><param><value><array><data>` +
		`<value><i4>0</i4></value><value><i4>96</i4></value>` +
		`<value><i4>1</i4></value><value><i4>0</i4></value>` +
		`<value><i4>5</i4></value>` +
		`</data></array></value></param></params></methodResponse>`)

	v, err := decodeResponse(data)
	if err != nil {
		t.Fatalf("decodeResponse: %v", err)
	}
	arr, ok := v.(arrayValue)
	if !ok || len(arr) != 5 {
		t.Fatalf("expected 5-element array, got %T %v", v, v)
	}
	if arr[1] != 96 || arr[4] != 5 {
		t.Errorf("array = %v", arr)
	}
}

func TestDecodeResponse_Latin1Umlaut(t *testing.T) {
	// ISO-8859-1 declared; 0xFC is 'ü'. Must decode to UTF-8 "Büro".
	data := []byte("<?xml version=\"1.0\" encoding=\"iso-8859-1\"?>" +
		"<methodResponse><params><param><value><struct>" +
		"<member><name>Device-Name</name><value><string>B\xfcro</string></value></member>" +
		"</struct></value></param></params></methodResponse>")

	v, err := decodeResponse(data)
	if err != nil {
		t.Fatalf("decodeResponse: %v", err)
	}
	s := v.(structValue)
	if got, _ := s.string("Device-Name"); got != "Büro" {
		t.Errorf("Device-Name = %q, want %q", got, "Büro")
	}
}

func TestEncodeCall(t *testing.T) {
	got := string(encodeCall("service.stm.sendTelegram", 0, 96, 1))
	want := `<?xml version="1.0"?><methodCall><methodName>service.stm.sendTelegram</methodName>` +
		`<params>` +
		`<param><value><i4>0</i4></value></param>` +
		`<param><value><i4>96</i4></value></param>` +
		`<param><value><i4>1</i4></value></param>` +
		`</params></methodCall>`
	if got != want {
		t.Errorf("encodeCall =\n%s\nwant\n%s", got, want)
	}
}

func TestParseEndpoint(t *testing.T) {
	cases := []struct {
		in       string
		wantHost string
		wantPort int
		wantErr  bool
	}{
		{"192.168.1.5", "192.168.1.5", 6680, false},
		{"192.168.1.5:6680", "192.168.1.5", 6680, false},
		{"host.local:80", "host.local", 80, false},
		{"", "", 0, true},
		{"host:0", "", 0, true},
		{"host:99999", "", 0, true},
	}
	for _, c := range cases {
		ep, err := ParseEndpoint(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("ParseEndpoint(%q) err=%v, wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if err == nil && (ep.Host != c.wantHost || ep.Port != c.wantPort) {
			t.Errorf("ParseEndpoint(%q) = %+v, want %s:%d", c.in, ep, c.wantHost, c.wantPort)
		}
	}
}
