package main

import "testing"

func TestTargetMatchesAnEntry(t *testing.T) {
	cases := []struct {
		entry, target string
		want          bool
	}{
		{"https://*.preview.crustdata.com", "https://pr-123.preview.crustdata.com", true},
		{"https://*.preview.crustdata.com", "https://preview.crustdata.com", false},
		{"https://*.preview.crustdata.com", "https://a.b.preview.crustdata.com", false},
		{"https://pr-*.preview.crustdata.com", "https://pr-123.preview.crustdata.com", true},
		{"https://pr-*.preview.crustdata.com", "https://qa-123.preview.crustdata.com", false},
		{"https://api.crustdata.com", "https://api.crustdata.com:443", true},
		{"https://api.crustdata.com:443", "https://api.crustdata.com", true},
		{"http://localhost", "http://localhost:80", true},
		{"https://api.crustdata.com", "https://API.Crustdata.com", true},
		{"https://*.preview.crustdata.com", "https://PR-1.Preview.crustdata.com", true},
		{"https://api.crustdata.com", "http://api.crustdata.com", false},
		{"https://api.crustdata.com", "https://api.crustdata.com:8443", false},
		{"http://localhost:8080", "http://localhost", false},
		{"http://10.0.0.5", "http://10.0.0.5:8080", false},
		{"http://[::1]:8080", "http://[::1]:8080", true},
	}
	for _, c := range cases {
		entry, err := parseTarget(c.entry)
		if err != nil {
			t.Fatalf("entry %s: %v", c.entry, err)
		}
		target, err := parseTarget(c.target)
		if err != nil {
			t.Fatalf("target %s: %v", c.target, err)
		}
		if got := entry.matches(target); got != c.want {
			t.Errorf("%s matches %s is %v, want %v", c.entry, c.target, got, c.want)
		}
	}
}
