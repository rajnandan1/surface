package main

import (
	"bytes"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func loadTest(t *testing.T, testYAML string) test {
	t.Helper()
	s, err := loadSuite("surface/api/users.yaml", "apiVersion: surface/v1\nkind: API\ntests:\n  - name: get-user\n    method: GET\n    path: /users/1\n"+testYAML)
	if err != nil {
		t.Fatal(err)
	}
	return s.Tests[0]
}

func checkTest(t *testing.T, testYAML string, status int, header http.Header, body string) []failedCheck {
	t.Helper()
	checks, _ := checkResponse(loadTest(t, testYAML), status, header, []byte(body), false)
	return checks
}

func lineOneOf(failure string) string {
	line, _, _ := strings.Cut(failure, "\n")
	return line
}

func texts(checks []failedCheck) []string {
	var texts []string
	for _, c := range checks {
		texts = append(texts, c.text)
	}
	return texts
}

func TestTheExampleBodyFollowsTheMatchTable(t *testing.T) {
	cases := []struct {
		match, example, response string
		want                     []string
	}{
		{"values", `{"a": 1}`, `{"a": 1, "b": 2}`, nil},
		{"types", `{"a": 1}`, `{"a": 1, "b": 2}`, nil},
		{"keys", `{"a": 1}`, `{"a": 1, "b": 2}`, nil},

		{"values", `{"a": "x"}`, `{}`, []string{`body.a: expected "x", key is missing`}},
		{"types", `{"a": "x"}`, `{}`, []string{`body.a: expected a string, key is missing`}},
		{"keys", `{"a": "x"}`, `{}`, []string{`body.a: key is missing`}},

		{"values", `{"a": "x"}`, `{"a": "y"}`, []string{`body.a: expected "x", got "y"`}},
		{"types", `{"a": "x"}`, `{"a": "y"}`, nil},
		{"keys", `{"a": "x"}`, `{"a": "y"}`, nil},

		{"values", `{"a": "x"}`, `{"a": 1}`, []string{`body.a: expected "x", got 1`}},
		{"types", `{"a": "x"}`, `{"a": 1}`, []string{`body.a: expected a string, got a number`}},
		{"keys", `{"a": "x"}`, `{"a": 1}`, nil},

		{"values", `{"a": null}`, `{"a": null}`, nil},
		{"values", `{"a": "x"}`, `{"a": null}`, []string{`body.a: expected "x", got null`}},
		{"values", `{"a": null}`, `{"a": "x"}`, []string{`body.a: expected null, got "x"`}},
		{"types", `{"a": "x"}`, `{"a": null}`, nil},
		{"types", `{"a": null}`, `{"a": "x"}`, nil},
		{"keys", `{"a": "x"}`, `{"a": null}`, nil},
		{"keys", `{"a": null}`, `{"a": "x"}`, nil},

		{"values", `[1, 2]`, `[1, 2]`, nil},
		{"values", `[1, 2, 3]`, `[1, 2]`, []string{`body: expected 3 items, got 2`}},
		{"values", `[1, 2]`, `[2, 1]`, []string{`body[0]: expected 1, got 2`, `body[1]: expected 2, got 1`}},
		{"types", `[{"id": 1}]`, `[{"id": 2}, {"id": "x"}, {}]`, []string{`body[1].id: expected a number, got a string`, `body[2].id: expected a number, key is missing`}},
		{"types", `[{"id": 1}]`, `[]`, nil},
		{"keys", `[{"id": 1}]`, `[{"id": "x"}, {}]`, []string{`body[1].id: key is missing`}},
		{"keys", `{"items": [{"id": 1}]}`, `{"items": [{"id": "x"}, {"name": "y"}]}`, []string{`body.items[1].id: key is missing`}},
		{"keys", `[{"id": 1}]`, `[]`, nil},

		{"values", `{"n": 1}`, `{"n": 1.0}`, nil},
		{"values", `{"n": 1.5}`, `{"n": 15e-1}`, nil},
		{"", `{"a": "x"}`, `{"a": "y"}`, []string{`body.a: expected "x", got "y"`}},
	}
	for _, c := range cases {
		match := ""
		if c.match != "" {
			match = "      match: " + c.match + "\n"
		}
		got := texts(checkTest(t, "    expect:\n      status: 200\n"+match+"      body: "+c.example+"\n", 200, nil, c.response))
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: example %s, response %s: failed checks are %q, want %q", c.match, c.example, c.response, got, c.want)
		}
	}
}

func TestIntegersAbove2To53CompareExactly(t *testing.T) {
	expect := "    expect:\n      status: 200\n      body: {\"id\": 9007199254740993}\n"
	if got := texts(checkTest(t, expect, 200, nil, `{"id": 9007199254740993}`)); got != nil {
		t.Errorf("the same id fails with %q", got)
	}
	want := []string{"body.id: expected 9007199254740993, got 9007199254740992"}
	if got := texts(checkTest(t, expect, 200, nil, `{"id": 9007199254740992}`)); !slices.Equal(got, want) {
		t.Errorf("failed checks are %q, want %q", got, want)
	}
}

func TestLineOneHasEveryFormOfTheFailureText(t *testing.T) {
	jsonHeader := http.Header{"Content-Type": {"application/json"}}
	long := strings.Repeat("a", 100)
	cases := []struct {
		expect  string
		status  int
		header  http.Header
		body    string
		overCap bool
		want    string
	}{
		{"expect: {status: 401}", 200, nil, "", false, "status: expected 401, got 200"},
		{`expect: {status: 200, headers: {Content-Type: application/json}}`, 200, http.Header{"Content-Type": {"text/html"}}, "", false, `headers.Content-Type: expected "application/json", got "text/html"`},
		{`expect: {status: 200, headers: {content-type: application/json}}`, 200, jsonHeader, "", false, ""},
		{`expect: {status: 201, headers: {Location: /users/1}}`, 201, jsonHeader, "", false, `headers.Location: expected "/users/1", header is missing`},
		{`expect: {status: 200, body: {"data": {"plan": "free"}}}`, 200, jsonHeader, `{"data": {"plan": "trial"}}`, false, `body.data.plan: expected "free", got "trial"`},
		{`expect: {status: 200, match: types, body: {"data": {"plan": "free"}}}`, 200, jsonHeader, `{"data": {"plan": 2}}`, false, `body.data.plan: expected a string, got a number`},
		{`expect: {status: 200, body: {"data": {"status": "active"}}}`, 200, jsonHeader, `{"data": {}}`, false, `body.data.status: expected "active", key is missing`},
		{`expect: {status: 200, body: {"items": [1, 2, 3]}}`, 200, jsonHeader, `{"items": [1, 2]}`, false, `body.items: expected 3 items, got 2`},
		{`expect: {status: 200, body: {"id": 1}}`, 200, nil, `<html>`, false, `body: not JSON`},
		{`expect: {status: 200, equal: {body.id: 1}}`, 200, nil, ``, false, `body: not JSON`},
		{`expect: {status: 200, body: {"id": 1}}`, 200, jsonHeader, `{"id": 1`, true, `body: response is over 10 MiB`},
		{"expect: {status: 200}", 200, nil, "aaaa", true, "body: response is over 10 MiB"},
		{"expect: {status: 200}\n    save: {user_id: body.data.id}", 200, jsonHeader, `{"data": {}}`, false, `save user_id: body.data.id finds nothing`},
		{"expect: {status: 200}\n    save: {first_item: \"body.items[3]\"}", 200, jsonHeader, `{"items": [1]}`, false, `save first_item: body.items[3] finds nothing`},
		{"expect: {status: 201}\n    save: {location: headers.Location}", 201, jsonHeader, ``, false, `save location: headers.Location finds nothing`},
		{`expect: {status: 200, equal: {body.data.id: 8812}}`, 200, jsonHeader, `{"data": {"id": 8813}}`, false, `body.data.id: expected 8812, got 8813`},
		{`expect: {status: 200, match: keys, equal: {body.data.id: 8812}}`, 200, jsonHeader, `{"data": {}}`, false, `body.data.id: expected 8812, key is missing`},
		{`expect: {status: 200, body: {"name": "` + long + `"}}`, 200, jsonHeader, `{"name": "b"}`, false, `body.name: expected "` + long[:79] + `…, got "b"`},
		{`expect: {status: 201, body: {"data": {"plan": "free", "status": "active"}}}`, 500, jsonHeader, `{"error": "database is down"}`, false, `status: expected 201, got 500 (+1 more)`},
	}
	for _, c := range cases {
		checks, _ := checkResponse(loadTest(t, "    "+c.expect+"\n"), c.status, c.header, []byte(c.body), c.overCap)
		if got := lineOne(checks); got != c.want {
			t.Errorf("%s: line one is %q, want %q", c.expect, got, c.want)
		}
	}
}

func TestA500WithAJSONErrorBodyReportsEveryFailedCheck(t *testing.T) {
	expect := "    expect:\n      status: 201\n      headers: {Content-Type: application/json}\n      body: {\"data\": {\"id\": 1, \"plan\": \"free\"}}\n      equal: {body.data.id: 8812}\n    save: {user_id: body.data.id}\n"
	checks := checkTest(t, expect, 500, http.Header{"Content-Type": {"application/json"}}, `{"error": {"code": "db_down"}}`)

	want := []failedCheck{
		{`status: expected 201, got 500`, []any{"status"}, `expected 201`, false},
		{`body.data: expected {"id":1,"plan":"free"}, key is missing`, []any{"body", "data"}, `"data" is missing, expected {"id":1,"plan":"free"}`, true},
		{`body.data.id: expected 8812, key is missing`, []any{"body", "data", "id"}, `"id" is missing, expected 8812`, true},
		{`save user_id: body.data.id finds nothing`, []any{"body", "data"}, `"data" is missing, needed by save user_id`, true},
	}
	if !slices.EqualFunc(checks, want, func(a, b failedCheck) bool {
		return a.text == b.text && slices.Equal(a.place, b.place) && a.mark == b.mark && a.missing == b.missing
	}) {
		t.Fatalf("failed checks are %v, want %v", checks, want)
	}
}

func TestSaveKeepsTheValuesForLaterTests(t *testing.T) {
	saving := loadTest(t, "    expect: {status: 201}\n    save: {user_id: body.data.id, location: headers.Location}\n")
	checks, saved := checkResponse(saving, 201, http.Header{"Location": {"/users/8812"}}, []byte(`{"data": {"id": 8812}}`), false)
	if checks != nil {
		t.Fatalf("failed checks are %v, want none", checks)
	}
	if want := map[string]json.RawMessage{"user_id": json.RawMessage(`8812`), "location": json.RawMessage(`"/users/8812"`)}; !maps.EqualFunc(saved, want, func(a, b json.RawMessage) bool { return bytes.Equal(a, b) }) {
		t.Fatalf("saved values are %v, want %v", saved, want)
	}
}

func TestARunReportsTheStatusAndEveryBodyFailureOfA500(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": {"code": "db_down"}}`))
	}))
	defer target.Close()

	result := runOneSuite(t, target.URL, header+`tests:
  - name: create-user
    method: POST
    path: /users
    expect:
      status: 201
      headers: {Content-Type: application/json}
      body: {"data": {"id": 1, "plan": "free"}}
      equal: {body.data.id: 8812}
`)

	if got, want := lineOneOf(oneTest(t, result).Failure), "status: expected 201, got 500 (+2 more)"; got != want {
		t.Fatalf("line one is %q, want %q", got, want)
	}
}
