package main

import (
	"slices"
	"testing"
)

const header = "apiVersion: surface/v1\nkind: API\n"

func TestLoadSuiteRejectsABrokenSuite(t *testing.T) {
	for _, c := range []struct{ rule, suite, want string }{
		{"an unknown field", header + `tests:
  - name: health
    method: GET
    path: /health
    expect:
      staus: 200
`, `line 8: unknown field "staus" (runner 1.0.1)`},
		{"two YAML documents", header + `tests:
  - name: health
    method: GET
    path: /health
    expect:
      status: 200
---
kind: API
`, `line 9: a suite holds one YAML document only`},
		{"an alias", header + `defaults:
  headers:
    Accept: &json application/json
tests:
  - name: health
    method: GET
    path: /health
    headers:
      Content-Type: *json
    expect:
      status: 200
`, `line 5: YAML anchors and aliases are not allowed in a suite`},
		{"apiVersion is missing", "kind: API\ntests: []\n", `apiVersion is missing`},
		{"an unknown apiVersion", "apiVersion: surface/v2\nkind: API\n", `line 1: unknown apiVersion "surface/v2" (runner 1.0.1)`},
		{"an unknown kind", "apiVersion: surface/v1\nkind: UI\n", `line 2: unknown kind "UI" (runner 1.0.1)`},
		{"no tests", header + "tests: []\n", `tests is missing or empty`},
		{"an unknown top-level field", header + "name: users\n", `line 3: unknown field "name" (runner 1.0.1)`},
		{"defaults with a field other than headers and timeout", header + `defaults:
  auth: token
`, `line 4: unknown field "auth" (runner 1.0.1)`},
		{"a test with no name", header + `tests:
  - method: GET
    path: /health
    expect:
      status: 200
`, `test 1: name is missing`},
		{"a name that is not a slug", header + `tests:
  - name: Health_Check
    method: GET
    path: /health
    expect:
      status: 200
`, `test 1: name "Health_Check" is not a slug of [a-z0-9-]`},
		{"a name used twice", header + `tests:
  - name: health
    method: GET
    path: /health
    expect:
      status: 200
  - name: health
    method: GET
    path: /healthz
    expect:
      status: 200
`, `test 2: name "health" is not unique in the suite`},
		{"a test with no method", header + `tests:
  - name: health
    path: /health
    expect:
      status: 200
`, `health: method is missing`},
		{"a test with no path", header + `tests:
  - name: health
    method: GET
    expect:
      status: 200
`, `health: path is missing`},
		{"a path with no leading slash", header + `tests:
  - name: health
    method: GET
    path: health
    expect:
      status: 200
`, `health: path "health" does not start with /`},
		{"a test with no expect.status", header + `tests:
  - name: health
    method: GET
    path: /health
    expect:
      headers:
        Content-Type: application/json
`, `health: expect.status is missing`},
		{"json and form in one test", header + `tests:
  - name: create-user
    method: POST
    path: /users
    json: {"email": "a@example.com"}
    form:
      email: a@example.com
    expect:
      status: 201
`, `create-user: set only one of json, form, and body`},
		{"a timeout over 5m", header + `tests:
  - name: create-user
    method: POST
    path: /users
    timeout: 10m
    expect:
      status: 201
`, `create-user: timeout 10m is over the limit of 5m`},
		{"a timeout that is not a Go duration", header + `tests:
  - name: create-user
    method: POST
    path: /users
    timeout: 10
    expect:
      status: 201
`, `create-user: timeout "10" is not a Go duration such as 10s`},
		{"a timeout of zero", header + `tests:
  - name: create-user
    method: POST
    path: /users
    timeout: 0s
    expect:
      status: 201
`, `create-user: timeout 0s is not above zero`},
		{"a defaults timeout over 5m", header + `defaults:
  timeout: 6m
tests:
  - name: health
    method: GET
    path: /health
    expect:
      status: 200
`, `defaults: timeout 6m is over the limit of 5m`},
		{"an unknown match mode", header + `tests:
  - name: search
    method: GET
    path: /search
    expect:
      status: 200
      match: fuzzy
      body: {"profiles": []}
`, `search: unknown match mode "fuzzy" (runner 1.0.1)`},
		{"a headers. path in expect.equal", header + `tests:
  - name: create-user
    method: POST
    path: /users
    expect:
      status: 201
      equal:
        headers.Location: /users/1
`, `line 10: expect.equal headers.Location: check a header with expect.headers`},
		{"an expect.equal path that does not start at body", header + `tests:
  - name: create-user
    method: POST
    path: /users
    expect:
      status: 201
      equal:
        data.id: 1
`, `line 10: expect.equal: "data.id" is not a path that starts at body, such as body.items[0].id or body["full.name"]`},
		{"a save path that does not start at body or headers", header + `tests:
  - name: create-user
    method: POST
    path: /users
    expect:
      status: 201
    save:
      user_id: data.id
`, `line 10: save user_id: "data.id" is not a path that starts at body or headers, such as body.items[0].id or headers.Location`},
		{"a save path with a bad index", header + `tests:
  - name: create-user
    method: POST
    path: /users
    expect:
      status: 201
    save:
      user_id: body.items[first].id
`, `line 10: save user_id: "body.items[first].id" is not a path that starts at body or headers, such as body.items[0].id or headers.Location`},
		{"a save that is not a map", header + `tests:
  - name: create-user
    method: POST
    path: /users
    expect:
      status: 201
    save: [body.id]
`, `line 9: save is not a map`},
		{"a needs name that is not in the suite", header + `tests:
  - name: create-user
    method: POST
    path: /users
    expect:
      status: 201
  - name: update-user
    needs: [create-usr]
    method: PATCH
    path: /users/1
    expect:
      status: 200
`, `update-user: needs create-usr, which is not in the suite`},
		{"a cycle in needs", header + `tests:
  - name: health
    method: GET
    path: /health
    expect:
      status: 200
  - name: create-user
    needs: [update-user]
    method: POST
    path: /users
    expect:
      status: 201
  - name: update-user
    needs: [health, create-user]
    method: PATCH
    path: /users/1
    expect:
      status: 200
`, `needs has a cycle: create-user needs update-user needs create-user`},
		{"an unknown variable", header + `tests:
  - name: create-user
    method: POST
    path: /users
    expect:
      status: 201
    save:
      user_id: body.id
  - name: update-user
    needs: [create-user]
    method: PATCH
    path: /users/${{ run.attempt }}
    expect:
      status: 200
    save:
      user_id: body.id
`, `line 14: unknown variable ${{ run.attempt }} (runner 1.0.1)`},
		{"a variable with an operator", header + `tests:
  - name: create-user
    method: POST
    path: /users
    expect:
      status: 201
    save:
      user_id: body.id
  - name: update-user
    needs: [create-user]
    method: PATCH
    path: /users/${{ run.id + 1 }}
    expect:
      status: 200
    save:
      user_id: body.id
`, `line 14: unknown variable ${{ run.id + 1 }} (runner 1.0.1)`},
		{"a variable with no closing braces", header + `tests:
  - name: create-user
    method: POST
    path: /users
    expect:
      status: 201
    save:
      user_id: body.id
  - name: update-user
    needs: [create-user]
    method: PATCH
    path: /users/${{ needs.create-user.user_id
    expect:
      status: 200
    save:
      user_id: body.id
`, `line 14: ${{ has no closing }}`},
		{"a needs variable with no saved value name", header + `tests:
  - name: create-user
    method: POST
    path: /users
    expect:
      status: 201
    save:
      user_id: body.id
  - name: update-user
    needs: [create-user]
    method: PATCH
    path: /users/${{ needs.create-user }}
    expect:
      status: 200
    save:
      user_id: body.id
`, `line 14: ${{ needs.create-user }} is not needs.<test>.<saved value>`},
		{"a needs variable for a test not in needs", header + `tests:
  - name: create-user
    method: POST
    path: /users
    expect:
      status: 201
    save:
      user_id: body.id
  - name: update-user
    method: PATCH
    path: /users/${{ needs.create-user.user_id }}
    expect:
      status: 200
    save:
      user_id: body.id
`, `line 13: ${{ needs.create-user.user_id }} reads create-user, which is not in needs`},
		{"a needs variable for a value the test does not save", header + `tests:
  - name: create-user
    method: POST
    path: /users
    expect:
      status: 201
    save:
      user_id: body.id
  - name: update-user
    needs: [create-user]
    method: PATCH
    path: /users/${{ needs.create-user.id }}
    expect:
      status: 200
    save:
      user_id: body.id
`, `line 14: ${{ needs.create-user.id }}: create-user has no saved value id`},
		{"a test that reads its own saved value", header + `tests:
  - name: create-user
    method: POST
    path: /users
    json: {"note": "${{ needs.create-user.user_id }}"}
    expect:
      status: 201
    save:
      user_id: body.id
  - name: update-user
    needs: [create-user]
    method: PATCH
    path: /users/1
    expect:
      status: 200
    save:
      user_id: body.id
`, `line 7: ${{ needs.create-user.user_id }} reads create-user, which is not in needs`},
		{"a cleanup that reads a test outside its test's needs", header + `tests:
  - name: create-user
    method: POST
    path: /users
    expect:
      status: 201
    save:
      user_id: body.id
    cleanup:
      method: DELETE
      path: /users/${{ needs.update-user.user_id }}
      expect:
        status: 204
  - name: update-user
    needs: [create-user]
    method: PATCH
    path: /users/1
    expect:
      status: 200
    save:
      user_id: body.id
`, `line 13: ${{ needs.update-user.user_id }} reads update-user, which is not in needs`},
		{"defaults that read a saved value", header + `defaults:
  headers:
    X-User: ${{ needs.create-user.user_id }}
tests:
  - name: create-user
    method: POST
    path: /users
    expect:
      status: 201
`, `line 5: ${{ needs.create-user.user_id }} reads create-user, which is not in needs`},
		{"a secret group that is not a slug", header + `defaults:
  headers:
    Authorization: Bearer ${{ secrets.Production.api_token }}
tests:
  - name: create-user
    method: POST
    path: /users
    expect:
      status: 201
`, `line 5: ${{ secrets.Production.api_token }} is not secrets.<group>.<name>, where <group> allows [a-z0-9-] and <name> allows [A-Za-z0-9_-]`},
		{"a cleanup with a name", header + `tests:
  - name: create-user
    method: POST
    path: /users
    expect:
      status: 201
    cleanup:
      name: delete-user
      method: DELETE
      path: /users/1
      expect:
        status: 204
`, `line 10: unknown field "name" (runner 1.0.1)`},
		{"a cleanup with needs", header + `tests:
  - name: create-user
    method: POST
    path: /users
    expect:
      status: 201
    cleanup:
      needs: [create-user]
      method: DELETE
      path: /users/1
      expect:
        status: 204
`, `line 10: unknown field "needs" (runner 1.0.1)`},
		{"a cleanup with no expect.status", header + `tests:
  - name: create-user
    method: POST
    path: /users
    expect:
      status: 201
    cleanup:
      method: DELETE
      path: /users/1
`, `create-user: cleanup: expect.status is missing`},
		{"a cleanup timeout over 5m", header + `tests:
  - name: create-user
    method: POST
    path: /users
    expect:
      status: 201
    cleanup:
      method: DELETE
      path: /users/1
      timeout: 6m
      expect:
        status: 204
`, `create-user: cleanup: timeout 6m is over the limit of 5m`},
		{"a secret name with a dot", header + `defaults:
  headers:
    Authorization: Bearer ${{ secrets.production.api.token }}
tests:
  - name: create-user
    method: POST
    path: /users
    expect:
      status: 201
`, `line 5: ${{ secrets.production.api.token }} is not secrets.<group>.<name>, where <group> allows [a-z0-9-] and <name> allows [A-Za-z0-9_-]`},
	} {
		t.Run(c.rule, func(t *testing.T) {
			_, err := loadSuite("surface/api/users.yaml", c.suite)
			want := "suite surface/api/users.yaml: " + c.want
			if err == nil || err.Error() != want {
				t.Fatalf("error is\n%v\nwant\n%s", err, want)
			}
		})
	}
}

func TestLoadSuiteReadsTheDesignExamples(t *testing.T) {
	for name, text := range map[string]string{"suite-format.md": suiteFormatExample, "data-safety.md": header + dataSafetyExample} {
		if _, err := loadSuite("surface/api/users.yaml", text); err != nil {
			t.Errorf("the example of %s does not load: %v", name, err)
		}
	}
}

func TestLoadSuiteParsesEachPathIntoSteps(t *testing.T) {
	s, err := loadSuite("surface/api/users.yaml", header+`tests:
  - name: search
    method: GET
    path: /search
    expect:
      status: 200
      equal:
        body.profiles[0]["full.name"]: Ada Lovelace
    save:
      location: headers.Location
`)
	if err != nil {
		t.Fatal(err)
	}
	test := s.Tests[0]
	if got, want := test.Expect.Equal[0].path.steps, []any{"body", "profiles", 0, "full.name"}; !slices.Equal(got, want) {
		t.Errorf("equal path steps are %#v, want %#v", got, want)
	}
	if got, want := test.Save[0].path.steps, []any{"headers", "Location"}; test.Save[0].name != "location" || !slices.Equal(got, want) {
		t.Errorf("saved value %q has steps %#v, want location with %#v", test.Save[0].name, got, want)
	}
}

const suiteFormatExample = `apiVersion: surface/v1          # The runner rejects an unknown version.
kind: API                       # HTTP tests. Later kinds add UI and MCP tests.

defaults:                       # Applies to every test. A test can override it.
  headers:
    Authorization: Bearer ${{ secrets.production.api_token }}  # Key api_token of secret group production. See secrets.md.
  timeout: 10s                  # Connect + send + wait + read. The runner default is 30s.

tests:
  # An independent test. It has no ` + "`" + `needs` + "`" + `, so it runs at the same time as the others.
  - name: search-person         # A slug [a-z0-9-], unique in this suite.
    method: POST
    path: /person/search        # Relative to the run's target.
    query:
      limit: 10                 # A number or a bool becomes text. A list repeats the key.
    json: {                     # Pasted JSON is valid YAML. Sets Content-Type if it is not set.
      "professional_network_profile_urls": ["https://www.linkedin.com/in/<example>/"]
    }
    timeout: 30s                # Overrides defaults.timeout.
    expect:
      status: 200
      headers:
        Content-Type: application/json   # Listed headers must match. Other headers are ignored.
      match: types              # values | types | keys. The default is values.
      body: {                   # The example body, pasted from a real response.
        "profiles": [
          {"name": "Ada Lovelace", "title": "Engineer", "location": null}
        ]
      }
      equal:
        body.profiles[0].name: Ada Lovelace

  - name: no-token-is-rejected
    method: POST
    path: /person/search
    headers:
      Authorization: null       # Removes the default header.
    expect:
      status: 401

  # A create-then-update pair.
  - name: create-user
    method: POST
    path: /users
    form:                       # A form-encoded body. Sets Content-Type if it is not set.
      email: u-${{ run.id }}@example.com   # Form values are text and are URL-escaped.
      plan: free
    expect:
      status: 201
    save:
      user_id: body.data.id     # If the path finds nothing, the test fails.
      location: headers.Location
    cleanup:                    # Removes what this test created. Runs after the suite's tests end. See data-safety.md.
      method: DELETE
      path: /users/${{ needs.create-user.user_id }}
      expect:
        status: 204

  - name: update-user
    needs: [create-user]        # Waits for create-user, and can read its saved values.
    method: PATCH
    path: /users/${{ needs.create-user.user_id }}   # Text, URL-escaped.
    json:
      user_id: ${{ needs.create-user.user_id }}     # The whole value: it stays a number.
      note: "updated by ${{ run.id }}"              # Inside text: it becomes text.
    expect:
      status: 200
      equal:
        body.id: ${{ needs.create-user.user_id }}
`

const dataSafetyExample = `tests:
  - name: create-org
    method: POST
    path: /orgs
    json: {"name": "org-${{ run.id }}"}        # run.id keeps two runs apart.
    expect:
      status: 201
    save:
      org_id: body.id
    cleanup:                                   # Removes what this test created.
      method: DELETE
      path: /orgs/${{ needs.create-org.org_id }}   # A cleanup reads its own test's saved values.
      expect:
        status: 204

  - name: create-user
    needs: [create-org]
    method: POST
    path: /orgs/${{ needs.create-org.org_id }}/users
    form:
      email: u-${{ run.id }}@example.com
    expect:
      status: 201
    save:
      user_id: body.data.id
    cleanup:
      method: DELETE
      path: /orgs/${{ needs.create-org.org_id }}/users/${{ needs.create-user.user_id }}
      expect:
        status: 204

  - name: update-user
    needs: [create-user]
    method: PATCH
    path: /users/${{ needs.create-user.user_id }}
    json: {"note": "updated by ${{ run.id }}"}
    expect:
      status: 200
`
