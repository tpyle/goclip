# Getting Started

## Install

```sh
go get github.com/tpyle/goclip
```

The import path is `github.com/tpyle/goclip`; the package name is
`clipboard`:

```go
import clipboard "github.com/tpyle/goclip"
```

(The explicit alias above is only there for readability - Go doesn't
require it, since the compiler uses the package's own `package clipboard`
declaration rather than the last element of the import path.)

## Writing to the clipboard

```go
if err := clipboard.Write("hello, clipboard"); err != nil {
	log.Fatal(err)
}
```

`Write` returns `ErrUnsupportedPlatform` if there's no backend available
for the current session - on Linux, that means neither `$WAYLAND_DISPLAY`
nor `$DISPLAY` is set (e.g. a headless SSH session or CI job with no
display server at all). On Windows and macOS, a real backend is always
available.

## The one thing every caller must do: call `MaybeRunHolder` first

On Linux, neither X11 nor Wayland treats the clipboard as a system
service - whichever process currently "owns" the clipboard selection has
to keep running to serve paste requests, for as long as it's the current
clipboard content. This is exactly why `xclip`/`wl-copy` fork themselves
into the background when you run them.

`goclip` does the same thing, natively: `Write` re-execs your program's
own binary with a private argv sentinel, detached into its own session,
and hands it the secret over an inherited pipe file descriptor. For that
re-exec to work, your `main()` needs to recognize it and hand off to the
holder loop **before** it does anything else - before flag parsing,
before a CLI framework's `Execute()`, before anything:

```go
func main() {
	if clipboard.MaybeRunHolder(os.Args[1:]) {
		return
	}
	// ... the rest of your program ...
}
```

If you skip this, `Write` will still try to re-exec your binary on
Linux - but since your `main()` won't recognize the sentinel argument,
the re-exec'd process will just run your normal program logic against
those unexpected arguments instead of holding the clipboard open, and
`Write` will time out waiting for a readiness signal that never comes.

On Windows and macOS, `MaybeRunHolder` always returns `false` immediately
and does nothing else - both clipboards are OS services that copy data at
set-time, so no helper process is ever spawned there. **Calling it
unconditionally in `main()` is still correct and required for portable
code** - it's how your program stays correct if it's ever built for
Linux, without needing a build-tag branch in your own code.

## What you get back

`Write`'s error, if any, already has context baked in (e.g. which Win32
call failed, or that the compositor doesn't support Wayland clipboard
access) - see [Platform Support](Platform-Support) for what those errors
mean per platform.

## Scope

`Write` (copy) only. There's no `Read` (paste) yet - see the top-level
README's Scope section for why.
