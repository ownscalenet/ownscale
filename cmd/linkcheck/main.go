// Command linkcheck checks that the console and the docs site still link to
// each other correctly: console source code links to doc pages (and
// anchors) that must still exist in the built docs site, and docs content
// links to console routes that must still exist in App.tsx.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run("frontend/src", "docs/content", "docsite/dist"); err != nil {
		fmt.Fprintln(os.Stderr, "linkcheck:", err)
		os.Exit(1)
	}
	fmt.Println("linkcheck: all console <-> docs links are valid")
}
