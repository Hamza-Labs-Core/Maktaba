//go:build !unix

package devmigrate

func freeBytes(string) int64 { return -1 }
