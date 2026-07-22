package stm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// maxResponseBytes bounds any XML-RPC response body (the project ZIP arrives in
// bounded chunks, so a single response is never this large in practice).
const maxResponseBytes = 24 << 20

// Endpoint is a resolved STM host and port. Port defaults to 6680.
type Endpoint struct {
	Host string
	Port int
}

// ParseEndpoint accepts "host" or "host:port"; a missing port defaults to 6680.
func ParseEndpoint(addr string) (Endpoint, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return Endpoint{}, fmt.Errorf("stm: empty address")
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		// Most likely "missing port": treat the whole input as the host.
		return Endpoint{Host: addr, Port: 6680}, nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return Endpoint{}, fmt.Errorf("stm: invalid port %q", portStr)
	}
	if host == "" {
		return Endpoint{}, fmt.Errorf("stm: empty host in %q", addr)
	}
	return Endpoint{Host: host, Port: port}, nil
}

func (e Endpoint) url() string { return fmt.Sprintf("http://%s:%d/", e.Host, e.Port) }

// Identity is the STM's self-description from service.stm.whoAreYou.
type Identity struct {
	STMAddress int
	FacilityID string
	DeviceID   string
	DeviceName string
}

// Client performs typed XML-RPC calls to one STM. One request per connection:
// the STM is HTTP/1.0 and closes after each response, so each sanitizingConn
// sees exactly one response.
type Client struct {
	endpoint Endpoint
	http     *http.Client

	mu            sync.Mutex
	lastSanitized bool
}

// NewClient builds a client whose transport dials the STM through the
// malformed-header sanitizer.
func NewClient(endpoint Endpoint) *Client {
	c := &Client{endpoint: endpoint}
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	c.http = &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives:      true,
			DisableCompression:     true,
			MaxResponseHeaderBytes: maxHeaderBytes,
			ResponseHeaderTimeout:  5 * time.Second,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				raw, err := dialer.DialContext(ctx, network, addr)
				if err != nil {
					return nil, err
				}
				return &sanitizingConn{Conn: raw, report: c.recordSanitized}, nil
			},
		},
	}
	return c
}

func (c *Client) recordSanitized(b bool) {
	c.mu.Lock()
	c.lastSanitized = b
	c.mu.Unlock()
}

// LastResponseSanitized reports whether the most recent response required the
// malformed-header fix — useful for the probe and health diagnostics.
func (c *Client) LastResponseSanitized() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastSanitized
}

// WhoAreYou calls service.stm.whoAreYou and returns the STM identity.
func (c *Client) WhoAreYou(ctx context.Context) (Identity, error) {
	v, err := c.call(ctx, "service.stm.whoAreYou")
	if err != nil {
		return Identity{}, err
	}
	s, ok := v.(structValue)
	if !ok {
		return Identity{}, fmt.Errorf("stm: whoAreYou: expected struct, got %T", v)
	}
	var id Identity
	id.STMAddress, _ = s.int("STM-Address")
	id.FacilityID, _ = s.string("Facility-ID")
	id.DeviceID, _ = s.string("Device-ID")
	id.DeviceName, _ = s.string("Device-Name")
	return id, nil
}

// FileChunk is one service.stm.readFile response: a slice of the project ZIP.
type FileChunk struct {
	Cur   int
	Total int
	CRC   int    // meaning not established; not used as an integrity check
	Bin   []byte // decoded from the base64 <bin> member
}

// ReadFile fetches one project chunk. The startup sequence uses readFile(0, i, 1).
func (c *Client) ReadFile(ctx context.Context, fileIndex, chunkIndex, mode int) (FileChunk, error) {
	v, err := c.call(ctx, "service.stm.readFile", fileIndex, chunkIndex, mode)
	if err != nil {
		return FileChunk{}, err
	}
	s, ok := v.(structValue)
	if !ok {
		return FileChunk{}, fmt.Errorf("stm: readFile: expected struct, got %T", v)
	}
	cur, okCur := s.int("cur")
	total, okTotal := s.int("total")
	if !okCur || !okTotal {
		return FileChunk{}, fmt.Errorf("stm: readFile: missing cur/total")
	}
	crc, _ := s.int("crc")
	bin, _ := s.bytes("bin")
	return FileChunk{Cur: cur, Total: total, CRC: crc, Bin: bin}, nil
}

// SendTelegram switches an output or reads module state; it returns the STM's
// 5-integer reply ([0, addr, toggle, ?, bitmask] for a state read).
func (c *Client) SendTelegram(ctx context.Context, stmIndex, moduleAddress, content int) ([]int, error) {
	v, err := c.call(ctx, "service.stm.sendTelegram", stmIndex, moduleAddress, content)
	if err != nil {
		return nil, err
	}
	arr, ok := v.(arrayValue)
	if !ok {
		return nil, fmt.Errorf("stm: sendTelegram: expected array, got %T", v)
	}
	if len(arr) != 5 {
		return nil, fmt.Errorf("stm: sendTelegram: expected 5 values, got %d", len(arr))
	}
	out := make([]int, len(arr))
	for i, e := range arr {
		n, ok := e.(int)
		if !ok {
			return nil, fmt.Errorf("stm: sendTelegram: element %d is %T, not int", i, e)
		}
		out[i] = n
	}
	return out, nil
}

// SimInputEvent simulates a rocker input (used for shutters, scenes, buttons).
// keyType is always 4 for the EMD rocker input. A non-fault response is success.
func (c *Client) SimInputEvent(ctx context.Context, stmIndex, module, channel, eventType, keyType int) error {
	_, err := c.call(ctx, "service.stm.simInputEvent", stmIndex, module, channel, eventType, keyType)
	return err
}

// RawWhoAreYou opens a bare TCP connection — no net/http, no sanitizer — sends a
// whoAreYou request, and returns the STM's raw, unmodified response bytes. This
// is the ground-truth capture used to reconcile transport fixtures against real
// hardware (plan §8 step 1). The returned bytes include the installation
// identity in the body, so callers must not persist or log them.
func RawWhoAreYou(ctx context.Context, ep Endpoint) ([]byte, error) {
	d := net.Dialer{Timeout: 3 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", ep.Host, ep.Port))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	body := encodeCall("service.stm.whoAreYou")
	req := fmt.Sprintf("POST / HTTP/1.1\r\nHost: %s:%d\r\n"+
		"Content-Type: application/x-www-form-urlencoded\r\n"+
		"Content-Length: %d\r\nConnection: close\r\n\r\n",
		ep.Host, ep.Port, len(body))
	if _, err := conn.Write(append([]byte(req), body...)); err != nil {
		return nil, err
	}
	// The STM is HTTP/1.0 and closes after the response, so read to EOF.
	return io.ReadAll(io.LimitReader(conn, maxResponseBytes+1))
}

func (c *Client) call(ctx context.Context, method string, params ...int) (value, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint.url(),
		bytes.NewReader(encodeCall(method, params...)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stm: %s: %w", method, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("stm: %s: reading body: %w", method, err)
	}
	if int64(len(data)) > maxResponseBytes {
		return nil, fmt.Errorf("stm: %s: response exceeds %d bytes", method, maxResponseBytes)
	}
	return decodeResponse(data)
}
