//go:build !darwin && !linux && !windows

package storage

import "errors"

func platformStatVolume(string) (volume, error) {
	return volume{}, errors.New("disks are not read on this OS")
}

func platformListVolumes() ([]volume, error) { return nil, nil }

func isSystemMount(mount string) bool { return mount == "/" }
