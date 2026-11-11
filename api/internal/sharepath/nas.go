package sharepath

import "github.com/Hamza-Labs-Core/Maktaba/shared/go/mediaaccess"

// RuleShare is the share a NAS's own folder is on by the platform's layout alone, and the
// share's folder (Epic 59 story 59.13; the rules are shared with setup:
// mediaaccess.RuleShare).
func RuleShare(platform, path string) (share, root string, ok bool) {
	return mediaaccess.RuleShare(platform, path)
}

// IsHostPath reports whether path is where the NAS itself keeps a share (false: a mount
// at another path, which nodes match by the share name the household gave).
func IsHostPath(platform, path string) bool { return mediaaccess.IsHostPath(platform, path) }
