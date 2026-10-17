package suite

import (
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type sentRequest struct {
	host, uri string
	header    http.Header
	body      string
}

// recordingTarget answers 200 and records each request it gets.
func recordingTarget(t *testing.T) (*httptest.Server, chan sentRequest) {
	t.Helper()
	requests := make(chan sentRequest, 10)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- sentRequest{r.Host, r.RequestURI, r.Header, string(body)}
	}))
	t.Cleanup(target.Close)
	return target, requests
}

const updateUserSuite = `apiVersion: surface/v1
kind: API
tests:
  - name: create-user
    method: POST
    path: /users
    expect:
      status: 201
    save:
      id: body.id
      age: body.age
      user: body.user
      manager: body.manager
  - name: update-user
    needs: [create-user]
    method: PATCH
    path: /users/${{ needs.create-user.id }}
    json:
      id: ${{ needs.create-user.id }}
      age: ${{ needs.create-user.age }}
      user: ${{ needs.create-user.user }}
      note: age ${{ needs.create-user.age }}, manager ${{ needs.create-user.manager }}
    expect:
      status: 200
`

var createUserValues = map[string]json.RawMessage{
	"needs.create-user.id":      json.RawMessage(`"a/b"`),
	"needs.create-user.age":     json.RawMessage(`36`),
	"needs.create-user.user":    json.RawMessage(`{"name":"Ada"}`),
	"needs.create-user.manager": json.RawMessage(`null`),
}

func TestNewRequestPutsVariableValuesIntoTheRequest(t *testing.T) {
	s, err := Load("surface/api/users.yaml", updateUserSuite)
	if err != nil {
		t.Fatal(err)
	}
	target, requests := recordingTarget(t)

	request, err := NewRequest(target.URL, s.Defaults, s.Tests[1].Request, createUserValues)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := send("01j9z3k4m5n6p7q8r9s0t1u2v3", request, "10s"); err != nil {
		t.Fatal(err)
	}

	got := <-requests
	if got.uri != "/users/a%2Fb" {
		t.Fatalf("the target got the path %q, want /users/a%%2Fb", got.uri)
	}
	if want := `{"id":"a/b","age":36,"user":{"name":"Ada"},"note":"age 36, manager null"}`; got.body != want {
		t.Fatalf("the target got the body %s, want %s", got.body, want)
	}
}

func TestAnObjectOrAnArrayInsideTextFailsTheTest(t *testing.T) {
	for _, c := range []struct{ value, want string }{
		{`{"name":"Ada"}`, "${{ needs.create-user.user }} is an object inside text"},
		{`["Ada"]`, "${{ needs.create-user.user }} is an array inside text"},
	} {
		s, err := Load("surface/api/users.yaml", strings.Replace(updateUserSuite, "note: age ${{ needs.create-user.age }}", "note: by ${{ needs.create-user.user }}", 1))
		if err != nil {
			t.Fatal(err)
		}
		values := maps.Clone(createUserValues)
		values["needs.create-user.user"] = json.RawMessage(c.value)

		_, err = NewRequest("http://127.0.0.1:1", s.Defaults, s.Tests[1].Request, values)

		if err == nil || err.Error() != c.want {
			t.Errorf("%s inside text: error is %v, want %s", c.value, err, c.want)
		}
	}
}
