package stm

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// A minimal XML-RPC codec, scoped to what the STM actually sends. It is
// deliberately not a general XML-RPC library. The Swift response parser
// (STMv3Client) is the behavioural reference for what these methods return.

// value is a decoded XML-RPC value: int, string, []byte (base64), arrayValue,
// or structValue.
type value any

type arrayValue []value

type structValue map[string]value

func (s structValue) int(name string) (int, bool) {
	switch t := s[name].(type) {
	case int:
		return t, true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		return n, err == nil
	}
	return 0, false
}

func (s structValue) string(name string) (string, bool) {
	switch t := s[name].(type) {
	case string:
		return t, true
	case int:
		return strconv.Itoa(t), true
	}
	return "", false
}

func (s structValue) bytes(name string) ([]byte, bool) {
	b, ok := s[name].([]byte)
	return b, ok
}

// Fault is a returned XML-RPC <fault>.
type Fault struct {
	Code    int
	Message string
}

func (f Fault) Error() string { return fmt.Sprintf("stm fault %d: %s", f.Code, f.Message) }

// encodeCall renders a <methodCall> with integer parameters (all the STM needs).
func encodeCall(method string, params ...int) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><methodCall><methodName>`)
	_ = xml.EscapeText(&b, []byte(method)) // constants, but escape defensively
	b.WriteString(`</methodName><params>`)
	for _, p := range params {
		b.WriteString(`<param><value><i4>`)
		b.WriteString(strconv.Itoa(p))
		b.WriteString(`</i4></value></param>`)
	}
	b.WriteString(`</params></methodCall>`)
	return []byte(b.String())
}

// decodeResponse returns the first <params> value, or a Fault error, from a
// <methodResponse> document.
func decodeResponse(data []byte) (value, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.CharsetReader = charsetReader
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil, fmt.Errorf("stm: response has no <params> or <fault>")
		}
		if err != nil {
			return nil, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "fault":
			v, err := firstValue(dec, "fault")
			if err != nil {
				return nil, err
			}
			s, ok := v.(structValue)
			if !ok {
				return nil, fmt.Errorf("stm: malformed <fault>")
			}
			code, _ := s.int("faultCode")
			msg, _ := s.string("faultString")
			return nil, Fault{Code: code, Message: msg}
		case "params":
			return firstValue(dec, "params")
		}
	}
}

// firstValue advances to the first <value> inside the currently-open container
// element and decodes it.
func firstValue(dec *xml.Decoder, container string) (value, error) {
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "value" {
				return decodeValue(dec)
			}
		case xml.EndElement:
			if t.Name.Local == container {
				return nil, fmt.Errorf("stm: no <value> inside <%s>", container)
			}
		}
	}
}

// decodeValue decodes the content of a <value> whose start element was already
// consumed. A <value> holds either one typed child or raw text (implicit string).
func decodeValue(dec *xml.Decoder) (value, error) {
	var text strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			return decodeTyped(dec, t)
		case xml.CharData:
			text.Write(t)
		case xml.EndElement: // </value> with only text
			return text.String(), nil
		}
	}
}

func decodeTyped(dec *xml.Decoder, start xml.StartElement) (value, error) {
	switch start.Name.Local {
	case "i4", "int":
		s, err := leafText(dec)
		if err != nil {
			return nil, err
		}
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("stm: bad integer %q", s)
		}
		return n, nil
	case "boolean":
		s, err := leafText(dec)
		if err != nil {
			return nil, err
		}
		return strings.TrimSpace(s) == "1", nil
	case "string", "name":
		return leafText(dec)
	case "base64":
		s, err := leafText(dec)
		if err != nil {
			return nil, err
		}
		raw, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(s), ""))
		if err != nil {
			return nil, fmt.Errorf("stm: bad base64: %w", err)
		}
		return raw, nil
	case "array":
		return decodeArray(dec)
	case "struct":
		return decodeStruct(dec)
	default:
		// Unknown typed tag: return its text so nothing silently vanishes.
		return leafText(dec)
	}
}

// leafText returns the concatenated character data of a leaf element (no child
// elements) whose start tag was already consumed.
func leafText(dec *xml.Decoder) (string, error) {
	var b strings.Builder
	depth := 1
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.StartElement:
			depth++
		case xml.EndElement:
			if depth--; depth == 0 {
				return b.String(), nil
			}
		}
	}
}

func decodeArray(dec *xml.Decoder) (value, error) {
	var out arrayValue
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "value" {
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				out = append(out, v)
			}
		case xml.EndElement:
			if t.Name.Local == "array" {
				return out, nil
			}
		}
	}
}

func decodeStruct(dec *xml.Decoder) (value, error) {
	out := structValue{}
	var name string
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "name":
				s, err := leafText(dec)
				if err != nil {
					return nil, err
				}
				name = strings.TrimSpace(s)
			case "value":
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				out[name] = v
			}
		case xml.EndElement:
			if t.Name.Local == "struct" {
				return out, nil
			}
		}
	}
}

// charsetReader converts the STM's declared ISO-8859-1 responses to UTF-8 so
// umlauts in device/room names survive. Code points U+0000–U+00FF are exactly
// Latin-1, so a byte-to-rune widening is a correct conversion.
func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
		return input, nil
	case "iso-8859-1", "iso8859-1", "iso_8859-1", "latin1", "latin-1":
		data, err := io.ReadAll(input)
		if err != nil {
			return nil, err
		}
		var b bytes.Buffer
		b.Grow(len(data))
		for _, c := range data {
			b.WriteRune(rune(c))
		}
		return &b, nil
	default:
		return nil, fmt.Errorf("stm: unsupported charset %q", charset)
	}
}
