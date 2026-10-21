package appruntime

import "github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"

// CompareVersions orders payload versions (-1, 0, 1) as every Maktaba version is
// ordered (layout.CompareVersions): a pre-release before its release, a `git
// describe` build after the tag it was built from (by its commits after it, a
// dirty tree after the clean build), never before it as a pre-release would be.
func CompareVersions(a, b string) int { return layout.CompareVersions(a, b) }
