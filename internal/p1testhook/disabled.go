//go:build !p1integration

package p1testhook

const Enabled = false

func emit(Event) bool { return false }
