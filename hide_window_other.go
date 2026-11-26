//go:build !windows

package main

// The Linux build has no tray yet, so closing the window quits the app.
const hideWindowOnClose = false
