package feed

import (
	"bytes"
	"encoding/xml"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// charsetReader provides decoding for the small set of non-UTF-8 encodings that
// still show up in feed documents. Anything else is rejected so the caller sees
// a clear error rather than mojibake.
func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
		return input, nil
	case "iso-8859-1", "latin1", "latin-1", "iso8859-1", "windows-1252", "cp1252":
		return newLatin1Reader(input), nil
	default:
		return nil, &UnsupportedCharsetError{Charset: charset}
	}
}

// UnsupportedCharsetError reports a feed encoding we cannot decode.
type UnsupportedCharsetError struct{ Charset string }

func (e *UnsupportedCharsetError) Error() string {
	return "unsupported charset: " + e.Charset
}

type latin1Reader struct {
	src io.Reader
	buf bytes.Buffer
}

func newLatin1Reader(r io.Reader) io.Reader { return &latin1Reader{src: r} }

func (l *latin1Reader) Read(p []byte) (int, error) {
	for l.buf.Len() == 0 {
		var b [1024]byte
		n, err := l.src.Read(b[:])
		for _, c := range b[:n] {
			l.buf.WriteRune(cp1252Rune(c))
		}
		if err != nil {
			if l.buf.Len() == 0 {
				return 0, err
			}
			break
		}
	}
	return l.buf.Read(p)
}

// cp1252High maps the 0x80-0x9F range, where windows-1252 differs from
// ISO-8859-1. Treating the two alike is what browsers do and is almost always
// what a mislabelled feed intended.
var cp1252High = [32]rune{
	0x20AC, 0x0081, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021,
	0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x008D, 0x017D, 0x008F,
	0x0090, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x009D, 0x017E, 0x0178,
}

func cp1252Rune(b byte) rune {
	if b >= 0x80 && b <= 0x9F {
		return cp1252High[b-0x80]
	}
	return rune(b)
}

// newDecoder returns an xml.Decoder configured the way feed documents require:
// HTML entity names are accepted (feeds routinely contain &nbsp; and friends)
// and legacy charsets are decoded.
func newDecoder(r io.Reader) *xml.Decoder {
	d := xml.NewDecoder(r)
	d.Strict = true
	d.Entity = xml.HTMLEntity
	d.CharsetReader = charsetReader
	return d
}

var entityRe = regexp.MustCompile(`&(#[0-9]+|#[xX][0-9a-fA-F]+|[a-zA-Z][a-zA-Z0-9]{1,31});`)

// DecodeEntities resolves HTML character references left over in text that was
// escaped twice by the publisher, e.g. "AT&amp;amp;T" or "Caf&eacute;". Text
// with no references is returned unchanged.
func DecodeEntities(s string) string {
	if !strings.ContainsRune(s, '&') {
		return s
	}
	return entityRe.ReplaceAllStringFunc(s, func(m string) string {
		name := m[1 : len(m)-1]
		if strings.HasPrefix(name, "#") {
			var (
				n   int64
				err error
			)
			if len(name) > 1 && (name[1] == 'x' || name[1] == 'X') {
				n, err = strconv.ParseInt(name[2:], 16, 32)
			} else {
				n, err = strconv.ParseInt(name[1:], 10, 32)
			}
			if err != nil || n <= 0 || n > 0x10FFFF {
				return m
			}
			return string(rune(n))
		}
		if v, ok := xml.HTMLEntity[name]; ok {
			return v
		}
		return m
	})
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

// StripTags removes markup from a fragment and collapses whitespace. It is used
// for title comparison during deduplication, never for output.
func StripTags(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = DecodeEntities(s)
	return strings.TrimSpace(multiSpace.ReplaceAllString(s, " "))
}
