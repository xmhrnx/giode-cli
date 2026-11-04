//go:build !windows

package progress

func enableVT() bool { return true }
