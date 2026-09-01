// Command basic is the minimal possible use of goclip: copy a fixed
// string to the system clipboard and exit.
//
// Run it with:
//
//	go run ./examples/basic
package main

import (
	"fmt"
	"log"
	"os"

	clipboard "github.com/tpyle/goclip"
)

func main() {
	// Required on every platform, not just Linux - see the Getting
	// Started wiki page for why this must run first, unconditionally,
	// before any other program logic.
	if clipboard.MaybeRunHolder(os.Args[1:]) {
		return
	}

	const text = "hello from goclip"
	if err := clipboard.Write(text); err != nil {
		log.Fatalf("clipboard.Write: %v", err)
	}
	fmt.Printf("copied %q to the clipboard\n", text)
}
