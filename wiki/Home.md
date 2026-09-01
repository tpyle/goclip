# goclip wiki

`goclip` copies text to the system clipboard without shelling out to any
external program (no `xclip`, `xsel`, `wl-copy`, or `pbcopy`) and without
cgo, on Linux, Windows, and macOS.

```go
import clipboard "github.com/tpyle/goclip"

func main() {
	if clipboard.MaybeRunHolder(os.Args[1:]) {
		return
	}
	if err := clipboard.Write("hello, clipboard"); err != nil {
		log.Fatal(err)
	}
}
```

That `MaybeRunHolder` line isn't optional on Linux - see
[Getting Started](Getting-Started) for why.

## Pages

* [Getting Started](Getting-Started) - installing the module, the
  `Write`/`MaybeRunHolder` API, and the one integration step every caller
  must do.
* [Platform Support](Platform-Support) - how each platform's backend
  works, what it depends on, and why.
* [Wayland](Wayland) - the `wlr-data-control-unstable-v1` protocol,
  compositor support, and a documented XWayland-bridging quirk.

See also the [`examples/`](../examples) directory for two small, runnable
programs.
