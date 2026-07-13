package host

import "runtime"

// DefaultBrowser returns the OS-default URL open command. The URL is
// appended as the final argument by the caller.
func DefaultBrowser() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"open"}
	default:
		return []string{"xdg-open"}
	}
}
