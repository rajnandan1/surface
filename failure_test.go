package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

const (
	docRunID  = "01j9z3k4m5n6p7q8r9s0t1u2v3"
	docTarget = "https://pr-123.preview.crustdata.com"
)

func docExample(t *testing.T, doc, testLine string) string {
	t.Helper()
	text, err := os.ReadFile("docs/design/" + doc)
	if err != nil {
		t.Fatal(err)
	}
	block, found := failureUnder(string(text), testLine)
	if !found {
		t.Fatalf("%s has no example under %q", doc, testLine)
	}
	return block
}

func failureUnder(log, testLine string) (string, bool) {
	_, after, found := strings.Cut(log, "\n"+testLine+"\n")
	var lines []string
	for _, line := range strings.Split(after, "\n") {
		line, indented := strings.CutPrefix(line, "  ")
		if !indented {
			break
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), found
}

type fakeResponse struct {
	status  string
	header  http.Header
	body    string
	elapsed time.Duration
}

func failureOf(t *testing.T, testYAML string, sent http.Header, r fakeResponse) string {
	t.Helper()
	s, err := loadSuite("surface/api/users.yaml", header+"tests:\n"+testYAML)
	if err != nil {
		t.Fatal(err)
	}
	test := s.Tests[0]
	runID, _ := json.Marshal(docRunID)
	request, err := newRequest(docTarget, s.Defaults, test.request, map[string]json.RawMessage{"run.id": runID})
	if err != nil {
		t.Fatal(err)
	}
	for name, values := range sent {
		request.Header[name] = values
	}
	var status int
	fmt.Sscan(r.status, &status)
	checks, _ := checkResponse(test, status, r.header, []byte(r.body), false)
	return failureText(checks, request, test.JSON.Kind != 0, &http.Response{Status: r.status, StatusCode: status, Header: r.header}, []byte(r.body), r.elapsed)
}

var jsonContent = http.Header{"Content-Type": {"application/json"}}

func TestTheFailureTextIsTheExampleOfEachDesignDoc(t *testing.T) {
	userAgent := http.Header{"User-Agent": {"surface/1.2.0 (run " + docRunID + ")"}}
	withToken := http.Header{"User-Agent": userAgent["User-Agent"], "Authorization": {"Bearer ${{ secrets.production.api_token }}"}}
	cases := []struct {
		doc, testLine, test string
		sent                http.Header
		response            fakeResponse
	}{
		{"failure-text.md", "surface/api/users.yaml create-user", `  - name: create-user
    method: POST
    path: /users
    form:
      email: u-${{ run.id }}@example.com
      plan: free
    expect:
      status: 201
      body: {"data": {"plan": "free", "status": "active"}}
`, withToken, fakeResponse{"201 Created", http.Header{"Content-Type": {"application/json"}, "Location": {"/users/8812"}}, `{"data": {"id": 8812, "email": "u-` + docRunID + `@example.com", "plan": "trial"}}`, 212 * time.Millisecond}},
		{"failure-text.md", "surface/api/search.yaml no-token-is-rejected", `  - name: no-token-is-rejected
    method: POST
    path: /person/search
    expect:
      status: 401
`, userAgent, fakeResponse{"200 OK", jsonContent, `{"profiles": []}`, 84 * time.Millisecond}},
		{"runs.md", "surface/api/users.yaml update-user", `  - name: update-user
    method: PATCH
    path: /users/8812
    json: {"user_id": 8812, "note": "updated by ${{ run.id }}"}
    expect:
      status: 200
`, withToken, fakeResponse{"404 Not Found", jsonContent, `{"error": "user 8812 not found"}`, 31 * time.Millisecond}},
		{"data-safety.md", "surface/api/users.yaml create-org cleanup", `  - name: create-org-cleanup
    method: DELETE
    path: /orgs/41
    expect:
      status: 204
`, userAgent, fakeResponse{"500 Internal Server Error", jsonContent, `{"error": "org has members"}`, 12 * time.Millisecond}},
	}
	for _, c := range cases {
		t.Run(c.doc+" "+c.testLine, func(t *testing.T) {
			want := docExample(t, c.doc, c.testLine)
			if got := failureOf(t, c.test, c.sent, c.response); got != want {
				t.Fatalf("failure text is\n%s\nwant\n%s", got, want)
			}
		})
	}
}

func TestEachCheckMarksTheLineItLookedAt(t *testing.T) {
	cases := []struct {
		name, expect string
		response     fakeResponse
		want         string
	}{
		{"a header with the wrong value, and a missing header",
			"expect: {status: 200, headers: {Content-Type: application/json, location: /users/1}}",
			fakeResponse{"200 OK", http.Header{"Content-Type": {"text/html"}}, "", 5 * time.Millisecond}, `
< 200 OK in 5ms                         ✓
< Content-Type: text/html               ✗ expected "application/json"
<                                       ✗ "location" is missing, expected "/users/1"`},
		{"a wrong array length, and a save path that finds nothing",
			"expect: {status: 200, body: {\"items\": [1, 2, 3]}}\n    save: {first: \"body.items[3]\", user_id: body.data.id}",
			fakeResponse{"200 OK", jsonContent, `{"items": [1, 2]}`, 5 * time.Millisecond}, `
< 200 OK in 5ms                         ✓
< Content-Type: application/json
<
< {
<   "items": [                          ✗ expected 3 items, got 2
<     1,
<     2,
<                                       ✗ [3] is missing, needed by save first
<   ],
<                                       ✗ "data" is missing, needed by save user_id
< }`},
		{"two checks on one line",
			"expect: {status: 200, body: {\"id\": 1}, equal: {body.id: 8812}}",
			fakeResponse{"200 OK", jsonContent, `{"id": 5}`, 5 * time.Millisecond}, `
< 200 OK in 5ms                         ✓
< Content-Type: application/json
<
< {
<   "id": 5                             ✗ expected 1
<                                       ✗ expected 8812
< }`},
		{"a missing key in an empty object, in types",
			"expect: {status: 200, match: types, body: {\"data\": {\"status\": \"active\"}}}",
			fakeResponse{"200 OK", jsonContent, `{"data": {}}`, 5 * time.Millisecond}, `
< 200 OK in 5ms                         ✓
< Content-Type: application/json
<
< {
<   "data": {}
<                                       ✗ "status" is missing, expected a string
< }`},
		{"an equal path whose object is missing",
			"expect: {status: 200, equal: {body.data.id: 8812}}",
			fakeResponse{"200 OK", jsonContent, `{"error": "db_down"}`, 5 * time.Millisecond}, `
< 200 OK in 5ms                         ✓
< Content-Type: application/json
<
< {
<   "error": "db_down"
< }
< ✗ body.data.id: expected 8812, key is missing`},
		{"a body that is not JSON",
			"expect: {status: 502, body: {\"id\": 1}}",
			fakeResponse{"200 OK", http.Header{"Content-Type": {"text/html"}}, "<html>\r\n<body>Bad gateway</body>\r\n</html>\r\n", 1234 * time.Millisecond}, `
< 200 OK in 1234ms                      ✗ expected 502
< Content-Type: text/html
<
< <html>                                ✗ not JSON
< <body>Bad gateway</body>
< </html>`},
		{"a body that is not UTF-8",
			"expect: {status: 200, body: {\"id\": 1}}",
			fakeResponse{"200 OK", nil, "\xff\xd8\xff\xe0", 5 * time.Millisecond}, `
< 200 OK in 5ms                         ✓
<
< <binary, 4 bytes>                     ✗ not JSON`},
		{"a check with no line",
			"expect: {status: 200, equal: {body.id: 1}}",
			fakeResponse{"200 OK", nil, "", 5 * time.Millisecond}, `
< 200 OK in 5ms                         ✓
< ✗ body: not JSON`},
		{"a long line",
			"expect: {status: 200, body: {\"email\": \"a@example.com\"}}",
			fakeResponse{"200 OK", jsonContent, `{"email": "someone-with-a-long-name@example.com"}`, 5 * time.Millisecond}, `
< 200 OK in 5ms                         ✓
< Content-Type: application/json
<
< {
<   "email": "someone-with-a-long-name@example.com"  ✗ expected "a@example.com"
< }`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			failure := failureOf(t, "  - name: get-user\n    method: GET\n    path: /users/1\n    "+c.expect+"\n", nil, c.response)
			_, got, _ := strings.Cut(failure, "\n> GET "+docTarget+"/users/1")
			if got != c.want {
				t.Fatalf("response is%s\nwant%s", got, c.want)
			}
		})
	}
}

func TestOverTheCapMarksTheFirstBodyLine(t *testing.T) {
	s, err := loadSuite("surface/api/users.yaml", header+"tests:\n  - name: export\n    method: GET\n    path: /export\n    expect: {status: 200}\n")
	if err != nil {
		t.Fatal(err)
	}
	request, _ := newRequest(docTarget, s.Defaults, s.Tests[0].request, nil)
	body := []byte("aaaa")
	checks, _ := checkResponse(s.Tests[0], 200, nil, body, true)
	want := "body: response is over 10 MiB\n> GET " + docTarget + "/export\n< 200 OK in 9ms                         ✓\n<\n< aaaa                                  ✗ over 10 MiB"
	if got := failureText(checks, request, false, &http.Response{Status: "200 OK"}, body, 9*time.Millisecond); got != want {
		t.Fatalf("failure text is\n%s\nwant\n%s", got, want)
	}
}

func TestABodyOf1MiBIsCutAt4KiBAndAFailedCheckPastTheCutKeepsItsPath(t *testing.T) {
	var body strings.Builder
	body.WriteString(`{"items": [`)
	for i := 0; body.Len() < 1<<20; i++ {
		if i > 0 {
			body.WriteString(", ")
		}
		price := 10
		if i == 412 {
			price = 12
		}
		fmt.Fprintf(&body, `{"price": %d}`, price)
	}
	body.WriteString("]}")

	failure := failureOf(t, "  - name: list-items\n    method: GET\n    path: /items\n    expect:\n      status: 200\n      equal:\n        body.items[412].price: 10\n", nil, fakeResponse{"200 OK", nil, body.String(), 5 * time.Millisecond})

	_, printed, _ := strings.Cut(failure, "\n< 200 OK in 5ms                         ✓\n<\n")
	lines := strings.Split(printed, "\n")
	if len(lines) < 3 || lines[len(lines)-1] != "< ✗ body.items[412].price: expected 10, got 12" {
		t.Fatalf("the failure text does not end with the check past the cut:\n%s", printed[max(0, len(printed)-300):])
	}
	var indented bytes.Buffer
	json.Indent(&indented, []byte(body.String()), "", "  ")
	all := strings.Split(indented.String(), "\n")
	shown := 0
	for i, line := range lines[:len(lines)-2] {
		if line != "< "+all[i] {
			t.Fatalf("body line %d is %q, want %q", i, line, "< "+all[i])
		}
		shown += len(all[i]) + 1
	}
	if next := len(all[len(lines)-2]) + 1; shown > 4096 || shown+next <= 4096 {
		t.Fatalf("the body shows %d bytes, want the most whole lines in 4096", shown)
	}
	if want := fmt.Sprintf("< … %d more bytes not shown", indented.Len()+1-shown); lines[len(lines)-2] != want {
		t.Fatalf("the cut line is %q, want %q", lines[len(lines)-2], want)
	}
}

func TestAJSONBodyKeepsTheKeyOrderOfTheResponse(t *testing.T) {
	body := `{"zebra": 1, "apple": {"yak": [], "bee": {}, "cat": [1, {"k": null}]}, "mango": "aé\"b", "big": 9007199254740993, "exp": 1.5e-3}`
	var lines []string
	printed, _ := bodyLines([]byte(body), true)
	for _, line := range printed {
		lines = append(lines, line.text)
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, []byte(body), "", "  "); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(lines, "\n"), indented.String(); got != want {
		t.Fatalf("printed body is\n%s\nwant\n%s", got, want)
	}
	if !strings.HasPrefix(lines[1], `  "zebra"`) || !strings.HasPrefix(lines[2], `  "apple"`) {
		t.Fatalf("keys are not in the order received:\n%s", strings.Join(lines, "\n"))
	}
}
