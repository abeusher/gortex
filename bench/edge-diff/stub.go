//go:build !ladybug

// Stub entry point for the non-ladybug build. The real edge-diff tool
// needs an on-disk Store to diff against memory; ladybug is the only
// persistent backend Gortex ships, so the diff is only meaningful when
// the binary is built with -tags ladybug.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "edge-diff requires the ladybug backend; rebuild with: go build -tags ladybug ./bench/edge-diff")
	os.Exit(2)
}
