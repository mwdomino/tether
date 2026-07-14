//go:build !darwin

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "tether-gui is only supported on macOS. Use 'tether status' on other platforms.")
	os.Exit(1)
}
