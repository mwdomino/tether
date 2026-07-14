package main

import _ "embed"

// Rope/knot icons, generated procedurally (see the design note in the repo).
// Bytes only — no GUI dependency — so this file also compiles in the
// non-darwin stub build.

//go:embed assets/appicon.png
var appIconPNG []byte

//go:embed assets/tray_green.png
var trayGreenPNG []byte

//go:embed assets/tray_amber.png
var trayAmberPNG []byte

//go:embed assets/tray_red.png
var trayRedPNG []byte

//go:embed assets/tray_grey.png
var trayGreyPNG []byte
