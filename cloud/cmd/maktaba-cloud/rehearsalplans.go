//go:build rehearsalplans

package main

import "github.com/Hamza-Labs-Core/Maktaba/cloud/rehearsal/plans"

// The cloud rehearsal's plans build (Epic 50 story 50.19, scenario relay-plans): the paid
// plans' TBD numbers set to the rehearsal's (rehearsal/plans). Never published: only
// tools/rehearsal-cloud/Dockerfile builds it, with BUILD_TAGS=rehearsalplans.
func init() { plans.Apply() }
