//go:build !linux && !darwin

package hostload

func loadAvg() ([3]float64, bool) { return [3]float64{}, false }
