//go:build !darwin

package devmigrate

// cloneOrCopy copies (clones are an APFS feature).
func cloneOrCopy(from, to string) error { return copyTree(from, to) }

func sameVolume(string, string) bool { return false }
