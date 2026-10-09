package main

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

type target struct {
	scheme, host, port string
	rate               int
}

func parseTarget(text string) (target, error) {
	malformed := fmt.Errorf("target %q is not scheme://host[:port] with the scheme http or https", text)
	u, err := url.Parse(text)
	switch {
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" || u.Hostname() == "":
		return target{}, malformed
	case u.User != nil:
		return target{}, fmt.Errorf("target %q must not have a user name", text)
	case u.Path != "":
		return target{}, fmt.Errorf("target %q must not have a path", text)
	case u.RawQuery != "" || u.ForceQuery:
		return target{}, fmt.Errorf("target %q must not have a query", text)
	case strings.Contains(text, "#"):
		return target{}, fmt.Errorf("target %q must not have a fragment", text)
	}
	port := map[string]string{"http": "80", "https": "443"}[u.Scheme]
	if u.Port() != "" {
		n, err := strconv.Atoi(u.Port())
		if err != nil || n < 1 || n > 65535 {
			return target{}, malformed
		}
		port = strconv.Itoa(n)
	}
	return target{scheme: u.Scheme, host: strings.ToLower(u.Hostname()), port: port}, nil
}

func (e target) matches(t target) bool {
	host := "^" + strings.ReplaceAll(regexp.QuoteMeta(e.host), `\*`, `[^.]+`) + "$"
	return e.scheme == t.scheme && e.port == t.port && regexp.MustCompile(host).MatchString(t.host)
}
