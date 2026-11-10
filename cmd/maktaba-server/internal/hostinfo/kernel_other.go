//go:build !linux

package hostinfo

func uname() Uname { return Uname{} }

func landlockABI() int { return 0 }

func userNamespaces() *bool { return nil }
