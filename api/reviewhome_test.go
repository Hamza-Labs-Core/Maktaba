package main

import (
	"io"
	"strings"
	"testing"
)

// Epic 58 story 58.04: the review home's commands read the passwords from the environment
// only, with the accounts' default names.
func TestReviewHomeAccountsFromEnv(t *testing.T) {
	env := map[string]string{envViewerPassword: "viewer-pass-from-secrets", envChildPassword: "child-pass-from-secrets"}
	a := accountsFromEnv(func(k string) string { return env[k] })
	if a.Viewer != "reviewer" || a.Child != "reviewer-child" || a.ViewerPassword != env[envViewerPassword] || a.ChildPassword != env[envChildPassword] {
		t.Errorf("defaults: %+v", a)
	}
	env[envViewer], env[envChild] = " store-reviewer ", "store-reviewer-kid"
	if a := accountsFromEnv(func(k string) string { return env[k] }); a.Viewer != "store-reviewer" || a.Child != "store-reviewer-kid" {
		t.Errorf("names from the environment: %+v", a)
	}
}

func TestReviewHomeRefusesBeforeTheDatabase(t *testing.T) {
	if err := runReviewHome(nil, io.Discard); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("no subcommand: %v", err)
	}
	if err := runReviewHome([]string{"grant-admin"}, io.Discard); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("an unknown subcommand: %v", err)
	}
	t.Setenv(envViewerPassword, "")
	t.Setenv(envChildPassword, "")
	if err := runReviewHome([]string{"rotate", "--dsn", "postgres://127.0.0.1:1/x"}, io.Discard); err == nil || !strings.Contains(err.Error(), "shorter than") {
		t.Errorf("no passwords in the environment: %v", err)
	}
}
