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
	newRunner(io.Discard).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/runs", strings.NewReader(body)))
	return response
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
