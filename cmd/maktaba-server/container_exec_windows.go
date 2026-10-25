package main

import "errors"

// execProcess: the container command runs in the Linux image only.
func execProcess(string, []string, []string) error {
	return errors.New("maktaba-server container runs in the Docker image (Linux)")
}
