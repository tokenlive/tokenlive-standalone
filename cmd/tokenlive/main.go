package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "tokenlive-standalone: scaffold only — all-in-one not implemented yet")
	fmt.Fprintln(os.Stderr, "see openspec/changes/merge-gateway-admin/")
	os.Exit(1)
}
