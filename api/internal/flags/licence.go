package flags

import (
	"fmt"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
)

// LicenceFeature is the licence feature behind key, a component's `licence` in the
// catalog (Epic 51 story 51.19): key must be a declared flag whose declaration has a
// Feature. A flag with no Feature (flags_admin, early_access) is not a licence: an
// admin could turn it on by an override, and a component gated by it would be a new
// paid gate.
func LicenceFeature(key string) (subscriptions.Feature, error) {
	d, ok := Lookup(key)
	if !ok {
		return "", fmt.Errorf("licence %q is not a flag of api/internal/flags/declarations.go", key)
	}
	if d.Feature == "" {
		return "", fmt.Errorf("licence %q is a flag with no licence feature", key)
	}
	return d.Feature, nil
}
