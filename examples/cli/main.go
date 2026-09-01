// Command cli is a slightly more realistic use of goclip: a small tool
// that copies its argument to the clipboard, structured the way a real
// program should be - clipboard.MaybeRunHolder runs before flag parsing,
// not after.
//
// Run it with:
//
//	go run ./examples/cli "some secret"
//	go run ./examples/cli -v "some secret"
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	clipboard "github.com/tpyle/goclip"
)

func main() {
	// This must be the very first thing main() does, before flag.Parse
	// or any other command-line handling - see the Getting Started wiki
	// page. On Linux, clipboard.Write re-execs this same binary into a
	// holder process; if that re-exec's arguments reached flag.Parse
	// instead of MaybeRunHolder, they'd be misinterpreted as normal
	// program input rather than recognized as the re-exec.
	if clipboard.MaybeRunHolder(os.Args[1:]) {
		return
	}

	verbose := flag.Bool("v", false, "print a confirmation message")
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: cli [-v] <text>")
		os.Exit(2)
	}
	text := flag.Arg(0)

	if err := clipboard.Write(text); err != nil {
		if errors.Is(err, clipboard.ErrUnsupportedPlatform) {
			fmt.Fprintln(os.Stderr, "no display server or clipboard API available here")
		} else {
			fmt.Fprintln(os.Stderr, "clipboard.Write:", err)
		}
		os.Exit(1)
	}

	if *verbose {
		fmt.Println("copied to clipboard")
	}
}
