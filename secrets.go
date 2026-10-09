package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

var serviceAccountDir = "/var/run/secrets/kubernetes.io/serviceaccount"

type group struct {
	secret  string
	callers []caller
	targets []target
}

func (pattern caller) matches(c caller) bool {
	glob := func(pattern, text string) bool {
		return regexp.MustCompile("^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, `[^/]*`) + "$").MatchString(text)
	}
	return glob(pattern.Repo, c.Repo) && glob(pattern.Ref, c.Ref)
}

// readGroups reads each group the suite at path reads a secret from into run.secrets, at most once per run, and returns why the suite fails at load.
func (r *runner) readGroups(run *runResult, path string, s suite, read map[string]error) error {
	for _, name := range s.secrets {
		groupName, key, _ := strings.Cut(strings.TrimPrefix(name, "secrets."), ".")
		if _, done := read[groupName]; !done {
			read[groupName] = r.readGroup(run, groupName)
		}
		value, found := run.secrets[name]
		switch {
		case read[groupName] != nil:
			return fmt.Errorf("suite %s: %v", path, read[groupName])
		case !found:
			return fmt.Errorf("suite %s: secret group %q has no key %q", path, groupName, key)
		case value == "":
			return fmt.Errorf("suite %s: secret group %q key %q is empty", path, groupName, key)
		}
	}
	return nil
}

func (r *runner) readGroup(run *runResult, name string) error {
	g, ok := r.config.groups[name]
	runTarget, _ := parseTarget(run.Target)
	switch {
	case !ok:
		return fmt.Errorf("secret group %q is not in the runner config", name)
	case !slices.ContainsFunc(g.callers, func(c caller) bool { return c.matches(run.Caller) }):
		return fmt.Errorf("secret group %q is not available to %s on %s", name, run.Caller.Repo, run.Caller.Ref)
	case !slices.ContainsFunc(g.targets, func(t target) bool { return t.matches(runTarget) }):
		return fmt.Errorf("secret group %q is not available to target %s", name, run.Target)
	}
	data, err := readSecret(g.secret)
	if err != nil {
		return fmt.Errorf("secret group %q was not read: %v", name, err)
	}
	for key, value := range data {
		run.secrets["secrets."+name+"."+key] = string(value)
	}
	return nil
}

// readSecret reads the token again on each call, because the kubelet rotates it.
func readSecret(name string) (map[string][]byte, error) {
	files := map[string][]byte{}
	for _, file := range []string{"token", "namespace", "ca.crt"} {
		content, err := os.ReadFile(filepath.Join(serviceAccountDir, file))
		if err != nil {
			return nil, err
		}
		files[file] = content
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(files["ca.crt"]) {
		return nil, errors.New("ca.crt holds no certificate")
	}
	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}, DisableKeepAlives: true},
	}
	host := net.JoinHostPort(os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT"))
	namespace := strings.TrimSpace(string(files["namespace"]))
	request, err := http.NewRequest(http.MethodGet, "https://"+host+"/api/v1/namespaces/"+url.PathEscape(namespace)+"/secrets/"+url.PathEscape(name), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(files["token"])))
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var secret struct {
		Data    map[string][]byte `json:"data"`
		Message string            `json:"message"`
	}
	err = json.NewDecoder(response.Body).Decode(&secret)
	switch {
	case response.StatusCode == http.StatusOK:
		return secret.Data, err
	case secret.Message != "":
		return nil, errors.New(secret.Message)
	}
	return nil, fmt.Errorf("the Kubernetes API answered %s", response.Status)
}

// newRedactor replaces each value and each form the runner escapes it to with its reference text, longest first, in one pass that never reads its own output.
func newRedactor(secrets map[string]string) *strings.Replacer {
	type form struct{ text, reference string }
	var forms []form
	for _, name := range slices.Sorted(maps.Keys(secrets)) {
		value := secrets[name]
		if value == "" {
			continue
		}
		quoted, _ := json.Marshal(value)
		for _, text := range []string{value, url.PathEscape(value), url.QueryEscape(value), string(quoted[1 : len(quoted)-1])} {
			forms = append(forms, form{text, "${{ " + name + " }}"})
		}
	}
	slices.SortStableFunc(forms, func(a, b form) int { return len(b.text) - len(a.text) })
	var oldnew []string
	for _, f := range forms {
		oldnew = append(oldnew, f.text, f.reference)
	}
	return strings.NewReplacer(oldnew...)
}
