package stm

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"testing"
	"time"
)

// serveOnce starts a loopback listener that answers exactly one request with the
// given raw response bytes, and returns its endpoint.
func serveOnce(t *testing.T, response []byte) Endpoint {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _ = conn.Read(make([]byte, 4096)) // drain request (best effort)
		_, _ = conn.Write(response)
	}()
	ep, err := ParseEndpoint(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return ep
}

// TestClient_WhoAreYou_EndToEnd drives the full path — NewClient, real
// http.Transport, the sanitizing dial, and the XML-RPC decode — against a raw
// TCP listener that emits the malformed STM response.
func TestClient_WhoAreYou_EndToEnd(t *testing.T) {
	client := NewClient(serveOnce(t, buildResponse(realDateLine)))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	id, err := client.WhoAreYou(ctx)
	if err != nil {
		t.Fatalf("WhoAreYou: %v", err)
	}
	if id.STMAddress != 0 || id.DeviceName != "Steuermodul 0" {
		t.Errorf("identity = %+v", id)
	}
	if !client.LastResponseSanitized() {
		t.Error("expected the malformed line to be removed on the end-to-end path")
	}
}

func TestClient_SendTelegram_EndToEnd(t *testing.T) {
	body := `<methodResponse><params><param><value><array><data>` +
		`<value><i4>0</i4></value><value><i4>96</i4></value>` +
		`<value><i4>1</i4></value><value><i4>0</i4></value>` +
		`<value><i4>5</i4></value>` +
		`</data></array></value></param></params></methodResponse>`
	client := NewClient(serveOnce(t, buildResponseBody(realDateLine, body)))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	got, err := client.SendTelegram(ctx, 0, 96, 1)
	if err != nil {
		t.Fatalf("SendTelegram: %v", err)
	}
	if len(got) != 5 || got[1] != 96 || got[4] != 5 {
		t.Errorf("telegram reply = %v", got)
	}
}

func TestClient_SendTelegram_RejectsWrongArrayLength(t *testing.T) {
	body := `<methodResponse><params><param><value><array><data>` +
		`<value><i4>0</i4></value><value><i4>96</i4></value>` +
		`</data></array></value></param></params></methodResponse>`
	client := NewClient(serveOnce(t, buildResponseBody(realDateLine, body)))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if _, err := client.SendTelegram(ctx, 0, 96, 1); err == nil {
		t.Fatal("short telegram reply should fail")
	}
}

func TestClient_ReadFile_EndToEnd(t *testing.T) {
	bin := base64.StdEncoding.EncodeToString([]byte("PKzip-bytes"))
	body := fmt.Sprintf(`<methodResponse><params><param><value><struct>`+
		`<member><name>cur</name><value><i4>0</i4></value></member>`+
		`<member><name>total</name><value><i4>1</i4></value></member>`+
		`<member><name>crc</name><value><i4>12345</i4></value></member>`+
		`<member><name>bin</name><value><base64>%s</base64></value></member>`+
		`</struct></value></param></params></methodResponse>`, bin)
	client := NewClient(serveOnce(t, buildResponseBody(realDateLine, body)))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	fc, err := client.ReadFile(ctx, 0, 0, 1)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if fc.Cur != 0 || fc.Total != 1 || string(fc.Bin) != "PKzip-bytes" {
		t.Errorf("chunk = %+v (bin=%q)", fc, fc.Bin)
	}
}

func TestClient_Fault_SurfacesError(t *testing.T) {
	body := `<methodResponse><fault><value><struct>` +
		`<member><name>faultCode</name><value><i4>7</i4></value></member>` +
		`<member><name>faultString</name><value><string>bad module</string></value></member>` +
		`</struct></value></fault></methodResponse>`
	client := NewClient(serveOnce(t, buildResponseBody(realDateLine, body)))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if _, err := client.SendTelegram(ctx, 0, 96, 1); err == nil {
		t.Error("expected the STM fault to surface as an error")
	}
}
