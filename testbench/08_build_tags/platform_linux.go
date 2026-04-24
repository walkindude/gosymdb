//go:build linux

package main

func platformSpecific() string {
	return linuxOnly()
}

func linuxOnly() string {
	return "linux implementation"
}

func helperForPlatform() string {
	return "linux helper"
}
