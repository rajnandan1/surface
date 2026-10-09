package main

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

const clockSkew = 60 * time.Second

type caller struct {
	Repo        string `json:"repo"`
	Ref         string `json:"ref"`
	SHA         string `json:"sha"`
	RunID       string `json:"run_id"`
	WorkflowRef string `json:"workflow_ref"`
}

type claims struct {
	Repository  string `json:"repository"`
	OwnerID     string `json:"repository_owner_id"`
	Ref         string `json:"ref"`
	SHA         string `json:"sha"`
	RunID       string `json:"run_id"`
	WorkflowRef string `json:"workflow_ref"`
}

func (r *runner) authorized(handle func(http.ResponseWriter, *http.Request, caller)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		claims, err := r.identify(req)
		if err != nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		if !slices.Contains(r.config.ownerIDs, claims.OwnerID) {
			http.Error(w, fmt.Sprintf("run: repo %q (owner id %s) may not use this runner", claims.Repository, claims.OwnerID), http.StatusForbidden)
			return
		}
		handle(w, req, caller{Repo: claims.Repository, Ref: claims.Ref, SHA: claims.SHA, RunID: claims.RunID, WorkflowRef: claims.WorkflowRef})
	}
}

func (r *runner) identify(req *http.Request) (claims, error) {
	var c claims
	scheme, raw, _ := strings.Cut(req.Header.Get("Authorization"), " ")
	if !strings.EqualFold(scheme, "Bearer") || raw == "" {
		return c, errors.New("run: token is missing")
	}
	token, err := r.verifier.Verify(req.Context(), raw)
	if expired, ok := errors.AsType[*oidc.TokenExpiredError](err); ok {
		return c, fmt.Errorf("run: token expired at %s", expired.Expiry.UTC().Format(time.RFC3339))
	}
	if err != nil {
		return c, errors.New("run: token signature is not valid")
	}
	if token.Issuer != r.config.issuer {
		return c, fmt.Errorf("run: token issuer %q is not %q", token.Issuer, r.config.issuer)
	}
	if !slices.Contains(token.Audience, r.config.url) {
		return c, fmt.Errorf("run: token audience %q is not this runner", strings.Join(token.Audience, ", "))
	}
	if err := token.Claims(&c); err != nil {
		return c, fmt.Errorf("run: token claims are not GitHub's: %v", err)
	}
	return c, nil
}
