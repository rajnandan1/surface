package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func postRun(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	newRunner(io.Discard, targetList(t, "https://api.crustdata.com", "https://*.preview.crustdata.com")).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/runs", strings.NewReader(body)))
	return response
}

func targetList(t *testing.T, entries ...string) []target {
	t.Helper()
	var list []target
	for _, entry := range entries {
		parsed, err := parseTarget(entry)
		if err != nil {
			t.Fatal(err)
		}
		list = append(list, parsed)
	}
	return list
}

func TestRunRequestWithABadTargetGetsOneLine(t *testing.T) {
	cases := []struct {
		body string
		code int
		line string
	}{
		{`{"suites": {}}`, 400, `run: target is missing`},
		{`{"target": "https://api.crustdata.com/v1", "suites": {}}`, 400, `run: target "https://api.crustdata.com/v1" must not have a path`},
		{`{"target": "https://api.crustdata.com/", "suites": {}}`, 400, `run: target "https://api.crustdata.com/" must not have a path`},
		{`{"target": "https://api.crustdata.com?debug=1", "suites": {}}`, 400, `run: target "https://api.crustdata.com?debug=1" must not have a query`},
		{`{"target": "https://api.crustdata.com#top", "suites": {}}`, 400, `run: target "https://api.crustdata.com#top" must not have a fragment`},
		{`{"target": "https://ci@api.crustdata.com", "suites": {}}`, 400, `run: target "https://ci@api.crustdata.com" must not have a user name`},
		{`{"target": "https://*.preview.crustdata.com", "suites": {}}`, 400, `run: target "https://*.preview.crustdata.com" must not have a *`},
		{`{"target": "ftp://api.crustdata.com", "suites": {}}`, 400, `run: target "ftp://api.crustdata.com" is not scheme://host[:port] with the scheme http or https`},
		{`{"target": "https://api.crustdata.com:99999", "suites": {}}`, 400, `run: target "https://api.crustdata.com:99999" is not scheme://host[:port] with the scheme http or https`},
		{`{"target": "http://10.0.0.5:8080", "suites": {}}`, 403, `run: target "http://10.0.0.5:8080" is not in the target list`},
	}
	for _, c := range cases {
		response := postRun(t, c.body)
		if response.Code != c.code || response.Body.String() != c.line+"\n" {
			t.Errorf("%s gets %d %q, want %d %q", c.body, response.Code, response.Body.String(), c.code, c.line+"\n")
		}
	}
}

func TestRunRequestOver16MiBGets413(t *testing.T) {
	suite := strings.Repeat("a", 16<<20)
	response := postRun(t, `{"target": "http://127.0.0.1:1", "suites": {"surface/big.yaml": "`+suite+`"}}`)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status is %d, want 413", response.Code)
	}
}

func TestRunRequestWithAnUnknownFieldGets400NamingTheRunnerVersion(t *testing.T) {
	response := postRun(t, `{"target": "http://127.0.0.1:1", "suites": {}, "targets": []}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status is %d, want 400", response.Code)
	}
	want := `run: unknown field "targets" (runner 1.0.0)` + "\n"
	if response.Body.String() != want {
		t.Fatalf("body is %q, want %q", response.Body.String(), want)
	}
}
