package config

import (
	"os"
	"strings"
)

// EnvContainer names the container a server runs in, set by its image (the Docker
// install, deploy/docker/server: "docker"). There the image's entrypoint
// (`maktaba-server container`) runs the launcher in place of a service manager, so
// nothing installs a systemd unit or asks for linger, and the server updates the
// runtime on its volume by itself, as a headless Linux install does.
const EnvContainer = "MAKTABA_CONTAINER"

// ContainerDocker is EnvContainer's value in the Docker image.
const ContainerDocker = "docker"

// Container is the container this process runs in ("" outside one).
func Container() string { return strings.TrimSpace(os.Getenv(EnvContainer)) }
