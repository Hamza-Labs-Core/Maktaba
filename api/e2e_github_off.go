//go:build !e2etrust

package main

import "github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/schemaguard"

// e2eGitHubAPI: a release build reads GitHub itself (e2e_github.go is a test build's).
func e2eGitHubAPI() string { return "" }

// e2eUpdateFeed: a release build reads the release feed at its own address.
func e2eUpdateFeed() string { return "" }

// knownSchemaRev is the newest migration this code knows (serve's guard).
func knownSchemaRev() int64 { return schemaguard.SchemaRev }

// e2eAboutFeed: a release build's About page reads the release feed at its own address.
func e2eAboutFeed() string { return "" }
