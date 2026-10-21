package config

import "golang.org/x/sys/windows"

// errCrossDevice is what a rename to another volume fails with.
var errCrossDevice error = windows.ERROR_NOT_SAME_DEVICE
