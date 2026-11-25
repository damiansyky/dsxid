//go:build !windows

package app

func startTray(onOpen, onLock, onQuit func()) error { return nil }

func stopTray() {}
