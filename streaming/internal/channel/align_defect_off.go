//go:build !tvdefect

package channel

// checkBuildDefect refuses a BuildDefect in a build without the tvdefect tag:
// a deliberately broken output must never come from a release build.
func checkBuildDefect(d string) {
	if d != "" {
		panic("channel.BuildDefect=" + d + " is set in a build without the tvdefect tag")
	}
}

func init() { checkBuildDefect(BuildDefect) }
