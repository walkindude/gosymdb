//go:build darwin

package main

func platformSpecific() string {
	return darwinOnly()
}

func darwinOnly() string {
	return "darwin implementation"
}

func helperForPlatform() string {
	return "darwin helper"
}
