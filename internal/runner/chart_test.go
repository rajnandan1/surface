package runner

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rajnandan1/surface"
	"go.yaml.in/yaml/v3"
)

const chart = "../../charts/surface"

func helm(t *testing.T, args ...string) []byte {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm is not installed")
	}
	out, err := exec.Command("helm", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm %v: %v\n%s", args, err, out)
	}
	return out
}

func helmTemplate(t *testing.T, values string) map[string]map[string]any {
	t.Helper()
	path := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(path, []byte(values), 0o600); err != nil {
		t.Fatal(err)
	}
	objects := map[string]map[string]any{}
	decoder := yaml.NewDecoder(bytes.NewReader(helm(t, "template", "surface", chart, "-n", "surface", "-f", path)))
	for {
		var object map[string]any
		err := decoder.Decode(&object)
		if errors.Is(err, io.EOF) {
			return objects
		}
		if err != nil {
			t.Fatal(err)
		}
		if object != nil {
			objects[object["kind"].(string)] = object
		}
	}
}

func dig(t *testing.T, value any, path ...any) any {
	t.Helper()
	for _, step := range path {
		switch key := step.(type) {
		case string:
			m, ok := value.(map[string]any)
			if !ok {
				t.Fatalf("%v: %v is not a map", path, value)
			}
			value = m[key]
		case int:
			list, ok := value.([]any)
			if !ok || key >= len(list) {
				t.Fatalf("%v: %v has no item %d", path, value, key)
			}
			value = list[key]
		}
	}
	return value
}

func yamlValue(t *testing.T, text string) any {
	t.Helper()
	var value any
	if err := yaml.Unmarshal([]byte(text), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestChartPassesHelmLint(t *testing.T) {
	helm(t, "lint", chart)
}

func TestChartVersionAppVersionAndImageTagAreTheVERSIONFile(t *testing.T) {
	var meta struct {
		Version    string `yaml:"version"`
		AppVersion string `yaml:"appVersion"`
	}
	if err := yaml.Unmarshal(helm(t, "show", "chart", chart), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Version != surface.Version || meta.AppVersion != surface.Version {
		t.Fatalf("chart version is %q and appVersion is %q, want both %q from VERSION", meta.Version, meta.AppVersion, surface.Version)
	}
	image := dig(t, helmTemplate(t, "config: {}")["Deployment"], "spec", "template", "spec", "containers", 0, "image")
	if want := "ghcr.io/rajnandan1/surface:" + surface.Version; image != want {
		t.Fatalf("image is %v, want %s", image, want)
	}
}

func TestChartLetsTheRunnerGetEachGroupSecretAndNoOther(t *testing.T) {
	objects := helmTemplate(t, `
config:
  url: https://surface.example.com
  groups:
    production:
      secret: prod-api
      callers: [{repo: crustdata/api, ref: refs/heads/main}]
      targets: [https://api.crustdata.com]
    staging:
      callers: [{repo: crustdata/api, ref: refs/heads/main}]
      targets: [https://staging.crustdata.com]
`)
	wantRules := yamlValue(t, `
- apiGroups: [""]
  resources: [secrets]
  verbs: [get]
  resourceNames: [prod-api, staging]
`)
	if rules := dig(t, objects["Role"], "rules"); !reflect.DeepEqual(rules, wantRules) {
		t.Fatalf("Role rules are %v, want %v", rules, wantRules)
	}
	binding := objects["RoleBinding"]
	if ref := dig(t, binding, "roleRef", "name"); ref != "surface" {
		t.Fatalf("RoleBinding binds Role %v, want surface", ref)
	}
	wantSubjects := yamlValue(t, "[{kind: ServiceAccount, name: surface, namespace: surface}]")
	if subjects := dig(t, binding, "subjects"); !reflect.DeepEqual(subjects, wantSubjects) {
		t.Fatalf("RoleBinding subjects are %v, want %v", subjects, wantSubjects)
	}
}

func TestChartWithNoGroupsRendersNoRoleAndNoRoleBinding(t *testing.T) {
	for _, config := range []string{"config: {url: https://surface.example.com}", "config: {url: https://surface.example.com, groups: {}}"} {
		objects := helmTemplate(t, config)
		for _, kind := range []string{"Role", "RoleBinding"} {
			if objects[kind] != nil {
				t.Errorf("%s: the chart renders a %s, want none: an empty resourceNames allows get on every Secret", config, kind)
			}
		}
	}
}

func TestChartRoutesTheHostOfConfigURL(t *testing.T) {
	objects := helmTemplate(t, `
config: {url: "https://surface.example.com"}
ingress:
  enabled: true
  tls: {secretName: surface-tls}
httpRoute:
  enabled: true
  parentRefs: [{name: public, namespace: gateway}]
`)
	if host := dig(t, objects["Ingress"], "spec", "rules", 0, "host"); host != "surface.example.com" {
		t.Errorf("Ingress host is %v, want surface.example.com", host)
	}
	if hosts := dig(t, objects["Ingress"], "spec", "tls", 0, "hosts"); !reflect.DeepEqual(hosts, []any{"surface.example.com"}) {
		t.Errorf("Ingress TLS hosts are %v, want [surface.example.com]", hosts)
	}
	if hosts := dig(t, objects["HTTPRoute"], "spec", "hostnames"); !reflect.DeepEqual(hosts, []any{"surface.example.com"}) {
		t.Errorf("HTTPRoute hostnames are %v, want [surface.example.com]", hosts)
	}
}

func TestChartRendersNoIngressAndNoHTTPRouteByDefault(t *testing.T) {
	objects := helmTemplate(t, "config: {url: https://surface.example.com}")
	if objects["Ingress"] != nil || objects["HTTPRoute"] != nil {
		t.Fatal("the chart renders an Ingress or an HTTPRoute by default, want neither")
	}
}

func named(t *testing.T, list any, name string) map[string]any {
	t.Helper()
	items, _ := list.([]any)
	for _, item := range items {
		if m, ok := item.(map[string]any); ok && m["name"] == name {
			return m
		}
	}
	t.Fatalf("%v has no item named %q", list, name)
	return nil
}

func TestChartMountsTheResultsVolumeAtResultsDir(t *testing.T) {
	for config, want := range map[string]string{
		"config: {url: https://surface.example.com}":                                "/var/lib/surface/results",
		"config: {url: https://surface.example.com, results: {dir: /data/results}}": "/data/results",
	} {
		pod := dig(t, helmTemplate(t, config)["Deployment"], "spec", "template", "spec")
		mount := named(t, dig(t, pod, "containers", 0, "volumeMounts"), "results")
		if mount["mountPath"] != want {
			t.Errorf("%s: results mount path is %v, want %s", config, mount["mountPath"], want)
		}
	}
}

func TestChartWithoutPersistenceMountsAnEmptyDir(t *testing.T) {
	objects := helmTemplate(t, "config: {url: https://surface.example.com}\npersistence: {enabled: false}")
	volume := named(t, dig(t, objects["Deployment"], "spec", "template", "spec", "volumes"), "results")
	if !reflect.DeepEqual(volume, map[string]any{"name": "results", "emptyDir": map[string]any{}}) {
		t.Errorf("results volume is %v, want an emptyDir", volume)
	}
	if objects["PersistentVolumeClaim"] != nil {
		t.Error("the chart renders a PersistentVolumeClaim, want none")
	}
}

const exampleConfig = `
  url: https://surface.example.com
  github:
    owner_ids: [63745551]
  targets:
    - https://*.preview.crustdata.com
    - url: https://api.crustdata.com
      rate: 20
  groups:
    production:
      callers:
        - repo: crustdata/api
          ref: refs/heads/main
      targets:
        - https://api.crustdata.com
`

func TestChartConfigMapHoldsTheConfigUnchanged(t *testing.T) {
	text := dig(t, helmTemplate(t, "config:"+exampleConfig)["ConfigMap"], "data", "config.yaml")
	if got, want := yamlValue(t, text.(string)), yamlValue(t, exampleConfig); !reflect.DeepEqual(got, want) {
		t.Fatalf("config.yaml is\n%v\nwant\n%v", text, exampleConfig)
	}
}

func TestChartConfigChangeChangesTheChecksumAnnotation(t *testing.T) {
	checksum := func(config string) any {
		return dig(t, helmTemplate(t, config)["Deployment"], "spec", "template", "metadata", "annotations", "checksum/config")
	}
	before := checksum("config: {url: https://surface.example.com, targets: [https://api.example.com]}")
	after := checksum("config: {url: https://surface.example.com, targets: [https://api.example.com, https://staging.example.com]}")
	if before == nil || before == after {
		t.Fatalf("checksum/config is %v before the config change and %v after, want two different values", before, after)
	}
}

func TestChartSetsGOMEMLIMITTo90PercentOfTheMemoryLimit(t *testing.T) {
	for values, want := range map[string]string{
		"config: {}": "3686MiB",
		"config: {}\nresources: {limits: {memory: 512Mi}}": "460MiB",
	} {
		env := dig(t, helmTemplate(t, values)["Deployment"], "spec", "template", "spec", "containers", 0, "env")
		if got := named(t, env, "GOMEMLIMIT")["value"]; got != want {
			t.Errorf("%q: GOMEMLIMIT is %v, want %s", values, got, want)
		}
	}
}
