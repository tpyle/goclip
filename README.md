# goclip

`goclip` copies text to the system clipboard without shelling out to any
external program (no `xclip`, `xsel`, `wl-copy`, or `pbcopy`) and without
cgo, on Linux, Windows, and macOS.

```go
import "github.com/tpyle/goclip"

func main() {
	// Required on every platform - see "Why a helper process on Linux"
	// below for why this must run first, unconditionally.
	if clipboard.MaybeRunHolder(os.Args[1:]) {
		return
	}
	if err := clipboard.Write("hello, clipboard"); err != nil {
		log.Fatal(err)
	}
}
```

Note the package name is `clipboard`; the import path is
`github.com/tpyle/goclip`. See [`examples/`](examples) for two small,
runnable programs, and the [wiki](https://github.com/tpyle/goclip/wiki)
for the full documentation this README summarizes.

## Platform support

| Platform | How | External process | Helper process |
|---|---|---|---|
| Linux (X11) | ICCCM selections via [`jezek/xgb`](https://github.com/jezek/xgb) | No | Yes - see below |
| Linux (Wayland) | Hand-rolled `wlr-data-control-unstable-v1` client | No | Yes - see below |
| Windows | Win32 clipboard API (`user32.dll`/`kernel32.dll`) via `golang.org/x/sys/windows` | No | No |
| macOS | AppKit's `NSPasteboard` via the Objective-C runtime ([`ebitengine/purego`](https://github.com/ebitengine/purego)) | No | No |

On Linux, `Write` picks Wayland if `$WAYLAND_DISPLAY` is set, else X11 if
`$DISPLAY` is set, matching the precedence most clipboard tools use. If
neither is set, or on any other platform with no backend, `Write` returns
`ErrUnsupportedPlatform`.

### Why a helper process on Linux

On both X11 and Wayland, the clipboard isn't a system service - it's a
selection that whichever client currently "owns" it must keep serving to
paste requests for as long as it's the current clipboard content. This is
exactly why `xclip`/`wl-copy` fork themselves into the background: the
moment their process exits, so does the clipboard content they set.

`goclip` does the same thing, natively: `Write` re-execs the calling
program's own binary (`os.Executable()`) with a private, undocumented
argv sentinel, detached into its own session (`Setsid`), and hands it the
secret over an inherited pipe file descriptor - never as a command-line
argument or environment variable, both of which leak via
`/proc/<pid>/cmdline` and `/proc/<pid>/environ` to any other process
running as the same user. `Write` blocks briefly (up to two seconds) for
the helper to confirm it actually took ownership before returning.

Because of this, any program using `goclip` **must** call
`clipboard.MaybeRunHolder` as the very first thing in `main()`, before any
flag or command parsing:

```go
func main() {
	if clipboard.MaybeRunHolder(os.Args[1:]) {
		return
	}
	// ... normal program logic ...
}
```

On Windows and macOS, `MaybeRunHolder` always returns `false` immediately
- both clipboards are OS services that copy data at set-time, so no
helper process is ever spawned or needed there.

### Wayland limitations

Setting the clipboard without holding keyboard focus (the only sane way
for a short-lived CLI process to do it) requires a compositor to expose
the `wlr-data-control-unstable-v1` protocol extension - the same one
`wl-copy` itself depends on. It's supported by wlroots-based compositors
(sway, Hyprland) and newer KDE Plasma/KWin releases.
**GNOME/Mutter implements neither this protocol nor its
`ext-data-control-v1` successor**, so Wayland clipboard access fails
there with a clear error - this is a pre-existing compositor limitation,
not a regression versus `wl-copy`.

Verified manually against KDE Plasma 6.4.3/KWin: content set via
`wlr-data-control-unstable-v1` is correctly registered as the compositor's
real clipboard (confirmed via Klipper, KDE's native clipboard manager),
but KWin's XWayland bridge does not proactively sync data-control-set
content to X11 clients (`xsel`/`xclip` under XWayland kept reading stale
X11-side content until an X11 client itself wrote something) - this
matches `wl-copy`'s own long-documented behavior on the same compositors,
not a `goclip`-specific gap.

## Scope

`Write` (copy) only. Paste (`Read`) would require implementing the other
side of both selection protocols (X11 `ConvertSelection`, Wayland
data-offer/receive) - a comparable amount of work - and isn't implemented
yet.

## Testing

Pure logic (backend selection, the re-exec/handshake construction, and
the X11/Wayland event-dispatch logic) is unit-tested against fakes,
including a real `AF_UNIX` socketpair for the Wayland backend so its
`SCM_RIGHTS` file-descriptor passing is exercised for real, not mocked.
Real X11/Wayland/Win32/AppKit I/O needs a real display server or OS and
is covered by CI's per-OS runners instead (see `.github/workflows/ci.yml`)
plus manual verification before a release - see the checklist below.

### Manual verification checklist

- **Linux/X11**: run a program using `goclip` under a real X server,
  confirm the text pastes into another application, confirm the holder
  process (`ps aux | grep __goclip_holder__`) exits once a second copy
  supersedes it, and confirm it survives the calling terminal closing.
- **Linux/Wayland**: same, under a compositor that supports
  `wlr-data-control-unstable-v1` (e.g. sway, Hyprland, or a recent KDE
  Plasma session); separately confirm a clear error on GNOME.
- **Windows**: paste into Notepad.
- **macOS**: paste into TextEdit.
