# Platform Support

| Platform | How | External process | Helper process |
|---|---|---|---|
| Linux (X11) | ICCCM selections via [`jezek/xgb`](https://github.com/jezek/xgb) | No | Yes |
| Linux (Wayland) | Hand-rolled `wlr-data-control-unstable-v1` client | No | Yes |
| Windows | Win32 clipboard API (`user32.dll`/`kernel32.dll`) via `golang.org/x/sys/windows` | No | No |
| macOS | AppKit's `NSPasteboard` via the Objective-C runtime ([`ebitengine/purego`](https://github.com/ebitengine/purego)) | No | No |

On Linux, `Write` picks Wayland if `$WAYLAND_DISPLAY` is set, else X11 if
`$DISPLAY` is set - the same precedence most clipboard tools use. See
[Wayland](Wayland) for that backend's own page; the rest of this page
covers X11, Windows, and macOS.

## Linux (X11)

`Write` becomes the owner of the `CLIPBOARD` selection (the ICCCM
convention every X11 clipboard tool follows): it creates an invisible 1x1
window (selections are owned by windows, not bare connections), interns
the atoms it needs (`CLIPBOARD`, `TARGETS`, `UTF8_STRING`, `STRING`,
`TEXT`), and calls `SetSelectionOwner`. From there the [clipboard-holder
process](Getting-Started#the-one-thing-every-caller-must-do-call-mayberunholder-first)
answers `SelectionRequest` events (offering `TARGETS`, or the text itself
for a text target) until a `SelectionClear` event says another client took
over.

[`jezek/xgb`](https://github.com/jezek/xgb) (BSD-3-Clause) is the
maintained fork of the largely-dormant `BurntSushi/xgb` - pure Go,
generated X11 protocol bindings, no cgo. It's used only for this
selection-ownership subset of the protocol.

## Windows

Uses the same Win32 sequence every clipboard tool on Windows does -
`OpenClipboard`/`EmptyClipboard`/`SetClipboardData` with
`CF_UNICODETEXT`, backed by a `GlobalAlloc`/`GlobalLock`'d buffer - called
via `golang.org/x/sys/windows`'s `NewLazySystemDLL` (which loads
`user32.dll`/`kernel32.dll` from `%SystemRoot%\System32` only, closing a
DLL-search-order-hijacking vector a plain `syscall.NewLazyDLL` wouldn't).
`OpenClipboard`/`CloseClipboard` are wrapped in
`runtime.LockOSThread()`/`UnlockOSThread()`, since Windows requires both
calls to happen on the same OS thread.

Because Windows's clipboard is an OS service that copies the data at
set-time, `Write` is a single synchronous call sequence with nothing left
running afterward - no helper process, unlike Linux.

## macOS

Uses AppKit's `NSPasteboard` - `generalPasteboard`, `clearContents`,
`setString:forType:` with `NSPasteboardTypeString` - invoked directly
through the Objective-C runtime via
[`ebitengine/purego`](https://github.com/ebitengine/purego)'s `objc`
subpackage (`purego.Dlopen`/`objc.GetClass`/`objc.Send`), entirely without
cgo.

Like Windows, macOS's pasteboard is an OS service, so there's no helper
process on macOS either.

## Why no cgo, and why native instead of shelling out

cgo would mean every build needs a C toolchain for the target platform,
which defeats the point of a small, easily cross-compiled Go library -
`purego`'s whole premise (resolve and call C-ABI functions at runtime via
`dlopen`/`dlsym`, no link-time cgo) is what makes the macOS backend
possible without one. Shelling out to `xclip`/`xsel`/`wl-copy`/`pbcopy`
(what most Go clipboard libraries, including this one's predecessor in
[gokeys](https://github.com/tpyle/gokeys), have historically done on
Linux/macOS) works, but requires those tools to be installed and adds
process-spawn overhead on every call; `goclip` talks to each platform's
clipboard API or protocol directly instead.
