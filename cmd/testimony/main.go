// Command testimony captures think-aloud test sessions and turns them into
// aligned, analysable records. See docs/ for the documentation.
package main

import (
	"os"

	"github.com/REPPL/Testimony/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
