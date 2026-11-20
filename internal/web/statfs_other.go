//go:build !linux && !darwin

package web

func freeSpace(string) (uint64, bool) { return 0, false }
