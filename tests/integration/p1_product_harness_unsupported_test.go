//go:build p1integration && (!linux || !amd64)

package integration

import "testing"

func newP1ProductHarness(t *testing.T) productHarness {
	t.Helper()
	t.Fatal("this real product harness requires the explicitly verified Linux/amd64 core fixture")
	return nil
}
