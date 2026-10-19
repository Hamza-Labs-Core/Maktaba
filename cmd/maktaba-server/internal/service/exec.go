package service

import (
	"os"
	"os/exec"
	"strconv"
)

// runCmd runs a service-manager command with our stdio.
var runCmd = func(argv []string) error {
	c := exec.Command(argv[0], argv[1:]...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return c.Run()
}

func uidString() string { return strconv.Itoa(os.Getuid()) }
