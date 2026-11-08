// Command genicon writes the application icon PNG used to build the .icns
// file: go run ./scripts/genicon <size> <out.png>
package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/keiko233/waylauncher/internal/icon"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: genicon <size> <out.png>")
		os.Exit(2)
	}
	size, err := strconv.Atoi(os.Args[1])
	if err != nil || size < 16 {
		fmt.Fprintln(os.Stderr, "bad size")
		os.Exit(2)
	}
	if err := os.WriteFile(os.Args[2], icon.App(size), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
