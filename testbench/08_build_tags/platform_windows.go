//go:build windows

package main

func platformSpecific() string {
	return windowsOnly()
}

func windowsOnly() string {
	return "windows implementation"
}

func helperForPlatform() string {
	return "windows helper"
}
