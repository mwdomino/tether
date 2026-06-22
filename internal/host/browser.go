package host

import "runtime"

// DefaultBrowser returns the OS-default URL open command. The URL is
// appended as the final argument by the caller.
func DefaultBrowser() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"open"}
	case "windows":
		// `cmd /c start "" <url>` — the empty title arg avoids start
		// interpreting the URL as a window title when it contains spaces
		// or quotes.
		return []string{"cmd", "/c", "start", ""}
	default:
		return []string{"xdg-open"}
	}
}
