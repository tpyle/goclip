# Wayland

## The protocol

Setting the clipboard without holding keyboard focus - the only sane way
for a short-lived CLI process to do it - isn't part of core Wayland. It
needs a compositor extension, and `goclip` uses the same one `wl-copy`
itself does: [`wlr-data-control-unstable-v1`](https://wayland.app/protocols/wlr-data-control-unstable-v1).

`goclip` hand-rolls the small subset of the Wayland wire protocol this
needs, rather than depending on a third-party Wayland client library. Two
pure-Go candidates were considered and rejected:

* `rajveermalviya/go-wayland` - archived (unmaintained) as of March 2025.
* `neurlang/wayland` - needs the C library `libxkbcommon` for keyboard
  support (not fully cgo-free), and its coverage of
  `wlr-data-control-unstable-v1` specifically is unconfirmed.

The actual surface needed for copy-only is small: `wl_display`
(`get_registry`, `sync`), `wl_registry` (`global`, `bind`), a bound
`wl_seat` (just to pass its object ID along - no capability/keyboard
events are consumed), and `zwlr_data_control_manager_v1`/
`zwlr_data_control_source_v1`/`zwlr_data_control_device_v1` for creating
and offering a data source and setting it as the selection. The wire
format itself - a Unix-domain-socket stream of
`(object_id, size<<16|opcode, args...)` messages, with file descriptors
passed out-of-band via `SCM_RIGHTS` - is fully covered by the standard
library's `net.UnixConn.ReadMsgUnix`/`WriteMsgUnix` plus
`golang.org/x/sys/unix.UnixRights`/`ParseSocketControlMessage`. No new
dependency was needed.

## Compositor support

`wlr-data-control-unstable-v1` is supported by wlroots-based compositors
(sway, Hyprland) and newer KDE Plasma/KWin releases (which also expose the
near-identical `ext-data-control-v1` successor).

**GNOME/Mutter implements neither protocol at all.** `goclip`'s Wayland
backend fails there with a clear error rather than hanging - this is a
pre-existing compositor limitation, not a `goclip`-specific gap. It's the
same reason `wl-copy` itself doesn't work on stock GNOME Wayland sessions.

## A real-world finding: KWin's XWayland bridge

This was verified manually against a real KDE Plasma 6.4.3/KWin session,
not just assumed from the spec:

* Content set via `goclip`'s Wayland backend **is** correctly registered
  as the compositor's real clipboard - confirmed independently via
  Klipper, KDE's own clipboard manager, which immediately reflected it.
* However, an X11 client running under KWin's XWayland bridge (e.g.
  `xsel --clipboard --output`) kept returning **stale, X11-side** content
  instead of what was just set over Wayland - the bridge doesn't
  proactively sync data-control-originated selections to X11 clients.

If your program (or something downstream of it, like a paste target) runs
as an X11/XWayland application while `goclip` copies over Wayland, don't
be surprised if it doesn't see the update immediately. This matches
`wl-copy`'s own long-documented behavior on the same compositors - it
isn't something `goclip` can work around, since the gap is in the
compositor's bridge, not in how the clipboard is set.

## No idle timeout

The Linux clipboard-holder process (see [Getting
Started](Getting-Started)) keeps serving the copied text until superseded
by a new clipboard owner - matching `xclip`/`wl-copy` parity. It doesn't
clear itself after a delay the way some password managers (e.g.
`pass(1)`) do; if your application wants that behavior, implement the
timeout yourself and call `Write` again with empty text (or let another
process take ownership) once it expires.
