// Package stm speaks the PHC STM v3 XML-RPC-over-HTTP protocol.
//
// The STM emits one non-standard response header line: an HTTP date value with
// no "Date:" field name (e.g. "Thu, 11 Jun 2026 21: 56:00"), which Go's
// net/http rejects as a malformed header. Swift's URLSession tolerates this
// transparently, so there is NO Swift code to port here — this sanitizer is the
// single piece the bridge must originate rather than reproduce. See
// docs/GO_WEBSITE_BRIDGE_PLAN.md §8 (Reference authority + STM transport).
package stm

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"sync"
)

const (
	// maxHeaderBytes bounds the whole response header block; maxHeaderLine bounds
	// any single line. Both guard against a hostile or broken peer.
	maxHeaderBytes = 16 << 10
	maxHeaderLine  = 4 << 10
)

// bareDateLine matches the STM's one known defect: an HTTP date value with no
// "Date:" field name, optionally with a stray space after the hour ("21: 56").
// A normal "Date: ..." header does not match (it begins with the field name),
// so anything else malformed is left for net/http to reject.
var bareDateLine = regexp.MustCompile(
	`^(Mon|Tue|Wed|Thu|Fri|Sat|Sun), \d{1,2} ` +
		`(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) ` +
		`\d{4} \d{2}: ?\d{2}:\d{2}( GMT| UTC)?$`)

// sanitizingConn wraps a net.Conn and, on the first read, removes the STM's
// single malformed header line before the bytes reach net/http. The status
// line, every other header, and the entire body pass through untouched; all
// writes go straight to the underlying connection.
//
// It is correct even when TCP delivers the response one byte at a time: line
// assembly is delegated to bufio.Reader.
type sanitizingConn struct {
	net.Conn

	once        sync.Once
	reader      io.Reader
	initErr     error
	didSanitize bool

	// report, if set, is called once with whether the defect line was removed.
	report func(sanitized bool)
}

func (c *sanitizingConn) Read(p []byte) (int, error) {
	c.once.Do(func() {
		c.initErr = c.rewrite()
		if c.report != nil {
			c.report(c.didSanitize)
		}
	})
	if c.initErr != nil {
		return 0, c.initErr
	}
	return c.reader.Read(p)
}

// rewrite reads the header block, drops the known defect line if present, and
// arranges for subsequent reads to serve the sanitized headers followed by any
// already-buffered body bytes and then the live connection.
func (c *sanitizingConn) rewrite() error {
	br := bufio.NewReaderSize(c.Conn, maxHeaderBytes)

	statusLine, err := readLine(br)
	if err != nil {
		return fmt.Errorf("stm: reading status line: %w", err)
	}

	var out bytes.Buffer
	out.WriteString(statusLine)
	total := len(statusLine)

	first := true
	for {
		line, err := readLine(br)
		if err != nil {
			return fmt.Errorf("stm: reading response header: %w", err)
		}
		total += len(line)
		if total > maxHeaderBytes {
			return fmt.Errorf("stm: response header exceeds %d bytes", maxHeaderBytes)
		}

		// Only the first header line is eligible for removal, and only if it is
		// exactly the known defect shape.
		if first {
			first = false
			if bareDateLine.MatchString(strings.TrimRight(line, "\r\n")) {
				c.didSanitize = true
				continue
			}
		}

		out.WriteString(line)
		if line == "\r\n" || line == "\n" {
			break // end of header block
		}
	}

	c.reader = io.MultiReader(bytes.NewReader(out.Bytes()), br)
	return nil
}

// readLine returns one line including its trailing newline. A premature EOF
// (before the blank line that ends the header block) is an error.
func readLine(br *bufio.Reader) (string, error) {
	line, err := br.ReadString('\n')
	if len(line) > maxHeaderLine {
		return "", fmt.Errorf("header line exceeds %d bytes", maxHeaderLine)
	}
	if err != nil {
		return "", err
	}
	return line, nil
}
