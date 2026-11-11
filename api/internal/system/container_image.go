package system

import (
	"os"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// The Docker image against the volume's runtime (Epic 59 story 59.12). A NAS's catalog
// (TrueNAS app updates, Unraid's update check, Watchtower, a household's pull in Container
// Manager) moves the image's tag; the server updates the runtime on the volume by itself.
// The two live side by side: a newer image goes onto the volume at its next start, an older
// one never replaces a newer runtime. The container's entrypoint tells the server which
// image it was started from (cmd/maktaba-server/cmd_container.go); Settings → Updates says
// which release runs and why. Admins only: the image's release names what the household
// pulled, which no other user needs.

// The variables the container's entrypoint sets for the server and every role.
const (
	EnvImageVersion = "MAKTABA_IMAGE_VERSION"
	EnvImageRefused = "MAKTABA_IMAGE_REFUSED"
)

// Why the runtime's release runs rather than the image's, or that they are one.
const (
	ImageSame    = "same"          // the image's release is the runtime's
	ImageOlder   = "image_older"   // the image is older than this home's runtime: the runtime runs
	ImageRefused = "image_refused" // the image's release could not start here: the release before runs
	ImagePending = "image_pending" // the image is newer and goes on the volume at the container's next start
)

// ContainerImage is GET /api/system/updates' container block.
type ContainerImage struct {
	Runtime string `json:"runtime"`
	Image   string `json:"image"`
	// Running is "runtime" (the volume's own release, not the image's) or "image" (the
	// image's release is the one that runs).
	Running string `json:"running"`
	Reason  string `json:"reason"`
}

// containerImage is the block for a server whose release is runtime, from the container's
// environment (nil outside the Docker image, or before an entrypoint that says).
func containerImage(runtime string, getenv func(string) string) *ContainerImage {
	image := layout.NormalizeVersion(strings.TrimSpace(getenv(EnvImageVersion)))
	runtime = layout.NormalizeVersion(strings.TrimSpace(runtime))
	if image == "" || runtime == "" {
		return nil
	}
	c := &ContainerImage{Runtime: runtime, Image: image, Running: "runtime"}
	switch cmp := layout.CompareVersions(image, runtime); {
	case cmp == 0:
		c.Running, c.Reason = "image", ImageSame
	case cmp < 0:
		c.Reason = ImageOlder
	case strings.TrimSpace(getenv(EnvImageRefused)) == "1":
		c.Reason = ImageRefused
	default:
		c.Reason = ImagePending
	}
	return c
}

// containerImageOf is containerImage from this process's environment.
func containerImageOf(runtime string) *ContainerImage { return containerImage(runtime, os.Getenv) }
