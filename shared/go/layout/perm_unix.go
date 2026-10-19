//go:build !windows

package layout

import (
	"fmt"
	"os"
)

// checkNotShared refuses a path group or other users can write.
func checkNotShared(p string) error {
	st, err := os.Lstat(p)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrMissingRelease, err)
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s is a symlink", ErrUnsafePerm, p)
	}
	if st.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%w: %s is %v", ErrUnsafePerm, p, st.Mode().Perm())
	}
	return nil
}
