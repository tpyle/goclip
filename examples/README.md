# Examples

Two small, runnable programs demonstrating `goclip`. Both call
`clipboard.MaybeRunHolder` as the very first thing in `main()` - see
[Getting Started](../wiki/Getting-Started.md) for why that's required,
not optional, on every platform.

* [`basic/`](basic/main.go) - the minimal possible use: copy a fixed
  string and exit.

  ```sh
  go run ./examples/basic
  ```

* [`cli/`](cli/main.go) - a small tool that copies its argument, showing
  where `MaybeRunHolder` belongs relative to flag parsing and how to tell
  `ErrUnsupportedPlatform` (no display server/clipboard API available)
  apart from any other failure.

  ```sh
  go run ./examples/cli "some secret"
  go run ./examples/cli -v "some secret"
  ```

On Linux, running either example prints nothing further but leaves a
short-lived clipboard-holder process running in the background until the
clipboard is next overwritten - see [Getting
Started](../wiki/Getting-Started.md) and [Platform
Support](../wiki/Platform-Support.md) for why.
