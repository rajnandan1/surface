package suite

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const maxPrintedBody = 4 << 10

type printedLine struct {
	text   string
	at     string
	closes string
}

func failureText(checks []failedCheck, request *http.Request, requestJSON bool, response *http.Response, body []byte, elapsed time.Duration) string {
	var sent []byte
	if request.GetBody != nil {
		reader, _ := request.GetBody()
		sent, _ = io.ReadAll(reader)
	}
	out := []string{lineOne(checks), "> " + request.Method + " " + request.URL.String()}
	for _, line := range message(">", request.Header, sent, requestJSON) {
		if line.text != "" {
			out = append(out, line.text)
		}
	}
	if response != nil {
		status := printedLine{text: fmt.Sprintf("< %s in %dms", response.Status, elapsed.Milliseconds()), at: "status"}
		out = append(out, marked(append([]printedLine{status}, message("<", response.Header, body, true)...), checks)...)
	}
	return strings.Join(out, "\n")
}

func message(prefix string, header http.Header, body []byte, pretty bool) []printedLine {
	var lines []printedLine
	for _, name := range slices.Sorted(maps.Keys(header)) {
		for i, value := range header[name] {
			line := printedLine{text: prefix + " " + name + ": " + value}
			if i == 0 {
				line.at = placeText([]any{"headers", name})
			}
			lines = append(lines, line)
		}
	}
	lines = append(lines, printedLine{closes: "headers"})
	if len(body) == 0 {
		return lines
	}
	lines = append(lines, printedLine{text: prefix})
	shown, more := bodyLines(body, pretty)
	for _, line := range shown {
		line.text = prefix + " " + line.text
		lines = append(lines, line)
	}
	if more > 0 {
		lines = append(lines, printedLine{text: fmt.Sprintf("%s … %d more bytes not shown", prefix, more)})
	}
	return lines
}

func bodyLines(body []byte, pretty bool) (shown []printedLine, more int) {
	p := &bodyPrinter{}
	switch {
	case !utf8.Valid(body):
		p.add([]any{"body"}, nil, 0, fmt.Sprintf("<binary, %d bytes>", len(body)))
	case pretty && json.Valid(body):
		p.json(string(body))
	default:
		at := []any{"body"}
		for line := range strings.Lines(string(body)) {
			p.add(at, nil, 0, strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"))
			at = nil
		}
	}
	return p.lines, p.size - p.shown
}

type bodyPrinter struct {
	lines       []printedLine
	size, shown int
}

func (p *bodyPrinter) add(at, closes []any, depth int, parts ...string) {
	size := 2*depth + 1
	for _, part := range parts {
		size += len(part)
	}
	if p.size += size; p.size > maxPrintedBody {
		return
	}
	p.shown = p.size
	line := printedLine{text: strings.Repeat("  ", depth) + strings.Join(parts, "")}
	if at != nil {
		line.at = placeText(at)
	}
	if closes != nil {
		line.closes = placeText(closes)
	}
	p.lines = append(p.lines, line)
}

// json prints each token's bytes as received: a Go map loses the key order, and a float64 loses digits.
func (p *bodyPrinter) json(body string) {
	i := 0
	skip := func() { i = len(body) - len(strings.TrimLeft(body[i:], " \t\r\n,:")) }
	more := func() bool {
		skip()
		return i < len(body) && body[i] != '}' && body[i] != ']'
	}
	next := func() string {
		skip()
		start := i
		switch body[i] {
		case '{', '}', '[', ']':
			i++
		case '"':
			for i++; body[i] != '"'; i++ {
				if body[i] == '\\' {
					i++
				}
			}
			i++
		default:
			if n := strings.IndexAny(body[i:], " \t\r\n,]}"); n >= 0 {
				i += n
			} else {
				i = len(body)
			}
		}
		return body[start:i]
	}
	comma := func() string {
		if more() {
			return ","
		}
		return ""
	}
	var value func(place []any, depth int, key, colon string)
	value = func(place []any, depth int, key, colon string) {
		token := next()
		switch {
		case token != "{" && token != "[":
			p.add(place, nil, depth, key, colon, token, comma())
			return
		case !more():
			end := next()
			p.add(place, place, depth, key, colon, token, end, comma())
			return
		}
		p.add(place, nil, depth, key, colon, token)
		for n := 0; more(); n++ {
			if token == "[" {
				value(append(place, n), depth+1, "", "")
				continue
			}
			inner := next()
			var name string
			if p.size <= maxPrintedBody {
				json.Unmarshal([]byte(inner), &name)
			}
			value(append(place, name), depth+1, inner, ": ")
		}
		end := next()
		p.add(nil, place, depth, end, comma())
	}
	value([]any{"body"}, 0, "", "")
}

func marked(lines []printedLine, checks []failedCheck) []string {
	on, missing := make([][]string, len(lines)), make([][]string, len(lines))
	used := make([]bool, len(checks))
	for i, line := range lines {
		for j, c := range checks {
			switch {
			case used[j]:
			case !c.missing && line.at != "" && line.at == checkPlace(c.place):
				on[i], used[j] = append(on[i], "✗ "+c.mark), true
			case c.missing && line.closes != "" && line.closes == checkPlace(c.place[:len(c.place)-1]):
				missing[i], used[j] = append(missing[i], "✗ "+c.mark), true
			}
		}
		if line.at == "" && line.text != "" && len(missing[i]) > 0 {
			lines[i-1].text += ","
		}
	}

	var out []string
	markLines := func(marks []string) {
		for _, mark := range marks {
			out = append(out, withMark("<", mark))
		}
	}
	for i, line := range lines {
		marks := on[i]
		if line.at == "status" && len(marks) == 0 {
			marks = []string{"✓"}
		}
		if line.at == "" {
			markLines(missing[i])
		}
		switch {
		case line.text == "":
		case len(marks) > 0:
			out = append(out, withMark(line.text, marks[0]))
			marks = marks[1:]
		default:
			out = append(out, line.text)
		}
		markLines(marks)
		if line.at != "" {
			markLines(missing[i])
		}
	}
	for j, c := range checks {
		if !used[j] {
			out = append(out, "< ✗ "+c.text)
		}
	}
	return out
}

// checkPlace names a check's place as the lines name it: Go's client canonicalizes the names of response headers.
func checkPlace(place []any) string {
	if len(place) == 2 && place[0] == "headers" {
		if name, ok := place[1].(string); ok {
			return placeText([]any{"headers", http.CanonicalHeaderKey(name)})
		}
	}
	return placeText(place)
}

func withMark(text, mark string) string {
	return text + strings.Repeat(" ", max(40-utf8.RuneCountInString(text), 2)) + mark
}
