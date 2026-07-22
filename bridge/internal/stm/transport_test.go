package stm

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// buildResponse produces a synthetic STM whoAreYou response. firstLine is the
// header line placed immediately after the status line (empty for none).
//
// The header block is reconciled byte-for-byte with a raw, non-proxied capture
// from the real STM (2026-07-22, plan §8 step 1). Two findings corrected earlier
// guesses: the STM really does send "Proxy-Connection: close" (it is NOT a proxy
// artifact), and the real bare-date line has no stray space after the hour. Only
// the body here is synthetic/redacted; it must never carry real identity data.
// whoAreYouBody is a synthetic identity response (fake IDs; no real installation
// data). Device-Name is the generic default for any STM at address 0.
const whoAreYouBody = `<?xml version="1.0" encoding="iso-8859-1"?>` +
	`<methodResponse><params><param><value><struct>` +
	`<member><name>STM-Address</name><value><i4>0</i4></value></member>` +
	`<member><name>Facility-ID</name><value><string>abc123</string></value></member>` +
	`<member><name>Device-ID</name><value><string>0000</string></value></member>` +
	`<member><name>Device-Name</name><value><string>Steuermodul 0</string></value></member>` +
	`</struct></value></param></params></methodResponse>`

func buildResponse(firstLine string) []byte {
	return buildResponseBody(firstLine, whoAreYouBody)
}

// buildResponseBody wraps an arbitrary XML-RPC body in the reconciled STM header
// block, so other methods can be exercised through the real transport.
func buildResponseBody(firstLine, body string) []byte {
	var b bytes.Buffer
	b.WriteString("HTTP/1.0 200 OK\r\n")
	if firstLine != "" {
		b.WriteString(firstLine + "\r\n")
	}
	b.WriteString("Server: INFRATEC_CTM/3.0\r\n")
	b.WriteString("Content-Type: text/xml\r\n")
	fmt.Fprintf(&b, "Content-Length: %d\r\n", len(body))
	b.WriteString("Proxy-Connection: close\r\n") // genuinely emitted by the STM (verified raw)
	b.WriteString("Pragma: no-cache\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return b.Bytes()
}

func readThroughSanitizer(t *testing.T, raw []byte, chunk int) (*http.Response, *sanitizingConn) {
	t.Helper()
	sc := &sanitizingConn{Conn: &fakeConn{r: bytes.NewReader(raw), chunk: chunk}}
	resp, err := http.ReadResponse(bufio.NewReader(sc), nil)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	return resp, sc
}

// realDateLine is the exact bare-date defect from the 2026-07-22 raw capture.
const realDateLine = "Wed, 22 Jul 2026 09:38:01"

func TestSanitizer_RemovesBareDateLine(t *testing.T) {
	resp, sc := readThroughSanitizer(t, buildResponse(realDateLine), 0)
	defer resp.Body.Close()

	if !sc.didSanitize {
		t.Error("expected the defect line to be removed")
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte("Steuermodul 0")) {
		t.Errorf("body not preserved: %q", body)
	}
	if got := resp.Header.Get("Server"); got != "INFRATEC_CTM/3.0" {
		t.Errorf("later headers not preserved, Server=%q", got)
	}
	if got := resp.Header.Get("Proxy-Connection"); got != "close" {
		t.Errorf("Proxy-Connection not preserved, got %q", got)
	}
}

func TestSanitizer_StraySpaceDateVariant(t *testing.T) {
	// The plan's mitmproxy capture showed a stray space after the hour
	// ("21: 56:00"); the real STM omits it. The matcher must accept both.
	resp, sc := readThroughSanitizer(t, buildResponse("Thu, 11 Jun 2026 21: 56:00"), 0)
	defer resp.Body.Close()
	if !sc.didSanitize {
		t.Error("stray-space date variant should also be removed")
	}
}

func TestSanitizer_FragmentedOneByteAtATime(t *testing.T) {
	resp, sc := readThroughSanitizer(t, buildResponse(realDateLine), 1)
	defer resp.Body.Close()
	if !sc.didSanitize {
		t.Error("expected the defect line to be removed under 1-byte delivery")
	}
	if body, _ := io.ReadAll(resp.Body); !bytes.Contains(body, []byte("methodResponse")) {
		t.Errorf("body not preserved under fragmentation: %q", body)
	}
}

func TestSanitizer_ValidDateHeaderPreserved(t *testing.T) {
	resp, sc := readThroughSanitizer(t, buildResponse("Date: Thu, 11 Jun 2026 21:56:00 GMT"), 0)
	defer resp.Body.Close()
	if sc.didSanitize {
		t.Error("a well-formed Date: header must not be removed")
	}
	if got := resp.Header.Get("Date"); got == "" {
		t.Error("Date header should have been preserved")
	}
}

func TestSanitizer_NoOddLine(t *testing.T) {
	resp, sc := readThroughSanitizer(t, buildResponse(""), 0)
	defer resp.Body.Close()
	if sc.didSanitize {
		t.Error("nothing should be removed when there is no defect line")
	}
}

func TestSanitizer_UnrelatedMalformedLineNotRemoved(t *testing.T) {
	// A different malformed line (no colon) must be left for net/http to reject,
	// not silently swallowed.
	sc := &sanitizingConn{Conn: &fakeConn{r: bytes.NewReader(buildResponse("Garbage line without a colon"))}}
	if _, err := http.ReadResponse(bufio.NewReader(sc), nil); err == nil {
		t.Error("expected net/http to reject an unrelated malformed header line")
	}
	if sc.didSanitize {
		t.Error("must not report sanitizing an unrelated malformed line")
	}
}

func TestSanitizer_PrematureEOF(t *testing.T) {
	// Truncate mid-header-block: no blank line ever arrives.
	sc := &sanitizingConn{Conn: &fakeConn{r: bytes.NewReader([]byte("HTTP/1.0 200 OK\r\nServer: x\r\n"))}}
	if _, err := io.ReadAll(sc); err == nil {
		t.Error("expected an error on premature EOF within the header block")
	}
}

// --- a minimal in-memory net.Conn -------------------------------------------

type fakeConn struct {
	r     *bytes.Reader
	chunk int // if >0, return at most this many bytes per Read
}

func (c *fakeConn) Read(p []byte) (int, error) {
	if c.chunk > 0 && len(p) > c.chunk {
		p = p[:c.chunk]
	}
	return c.r.Read(p)
}
func (c *fakeConn) Write(p []byte) (int, error)        { return len(p), nil }
func (c *fakeConn) Close() error                       { return nil }
func (c *fakeConn) LocalAddr() net.Addr                { return fakeAddr{} }
func (c *fakeConn) RemoteAddr() net.Addr               { return fakeAddr{} }
func (c *fakeConn) SetDeadline(t time.Time) error      { return nil }
func (c *fakeConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *fakeConn) SetWriteDeadline(t time.Time) error { return nil }

type fakeAddr struct{}

func (fakeAddr) Network() string { return "fake" }
func (fakeAddr) String() string  { return "fake" }
