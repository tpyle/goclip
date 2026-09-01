//go:build linux

package clipboard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// holderSentinel is the private argv[0]-following marker that tells a
// re-exec'd copy of the calling program to run as a clipboard holder
// instead of its normal main(). It's deliberately not a documented flag
// or subcommand of anything - see MaybeRunHolder.
const holderSentinel = "__goclip_holder__"

// holderReadyTimeout bounds how long spawnHolder waits for the holder
// process to confirm it took ownership of the selection. A var, not a
// const, so tests can shrink it instead of waiting out the real timeout.
var holderReadyTimeout = 2 * time.Second

// MaybeRunHolder recognizes a re-exec into clipboard-holder mode (see
// spawnHolder) and, if args matches it, runs the holder to completion and
// returns true. Callers on Linux must invoke this as the very first thing
// in main(), before any flag/command parsing, e.g.:
//
//	if clipboard.MaybeRunHolder(os.Args[1:]) {
//		return
//	}
//
// It never returns for the sentinel case except by calling os.Exit -
// there's no other work for a holder process to do.
func MaybeRunHolder(args []string) bool {
	kind, ok := parseHolderArgs(args)
	if !ok {
		return false
	}
	runHolder(kind)
	return true
}

// parseHolderArgs is MaybeRunHolder's argument recognition, split out so
// it's unit-testable without the side effect of actually running (and
// os.Exit-ing) as a holder.
func parseHolderArgs(args []string) (kind backendKind, ok bool) {
	if len(args) < 2 || args[0] != holderSentinel {
		return 0, false
	}
	if args[1] == "wayland" {
		return backendWayland, true
	}
	return backendX11, true
}

// runHolder reads the secret from the inherited fd 3, takes ownership of
// the clipboard via the given backend, and serves it until superseded.
// fd 3 and fd 4 are the parent's secretR/readyW ends, inherited via
// exec.Cmd.ExtraFiles - see spawnHolder.
func runHolder(kind backendKind) {
	secretFile := os.NewFile(3, "goclip-secret")
	readyFile := os.NewFile(4, "goclip-ready")

	secret, err := io.ReadAll(secretFile)
	_ = secretFile.Close()
	if err != nil {
		os.Exit(1)
	}

	ready := func() {
		_, _ = readyFile.Write([]byte{1})
	}

	if err := newBackend(kind).serve(secret, ready); err != nil {
		os.Exit(1)
	}
}

// spawnHolder forks a detached copy of the running program into
// clipboard-holder mode (via MaybeRunHolder) and blocks until it confirms
// it took ownership of the selection, or holderReadyTimeout elapses.
//
// The secret is passed over an inherited pipe fd, never as an argv
// element or environment variable - both leak via /proc/<pid>/cmdline and
// /proc/<pid>/environ (readable by the same user on most systems), which
// matters here because the secret is a password.
func spawnHolder(kind backendKind, secret string) error {
	secretR, secretW, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("clipboard: create secret pipe: %w", err)
	}
	readyR, readyW, err := os.Pipe()
	if err != nil {
		_ = secretR.Close()
		_ = secretW.Close()
		return fmt.Errorf("clipboard: create readiness pipe: %w", err)
	}

	self, err := os.Executable()
	if err != nil {
		_ = secretR.Close()
		_ = secretW.Close()
		_ = readyR.Close()
		_ = readyW.Close()
		return fmt.Errorf("clipboard: find own executable: %w", err)
	}

	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		_ = secretR.Close()
		_ = secretW.Close()
		_ = readyR.Close()
		_ = readyW.Close()
		return fmt.Errorf("clipboard: open %s: %w", os.DevNull, err)
	}
	defer func() { _ = devNull.Close() }()

	cmd := buildHolderCommand(self, kind, secretR, readyW, devNull)
	startErr := cmd.Start()
	// The parent's copies of the fds handed to the child must be closed
	// regardless of Start's outcome - otherwise the parent's own
	// still-open ends would keep the pipes readable/writable forever,
	// breaking the EOF-based readiness/failure detection below.
	_ = secretR.Close()
	_ = readyW.Close()
	if startErr != nil {
		_ = secretW.Close()
		_ = readyR.Close()
		return fmt.Errorf("clipboard: start clipboard holder: %w", startErr)
	}

	_, writeErr := secretW.Write([]byte(secret))
	_ = secretW.Close()
	if writeErr != nil {
		_ = readyR.Close()
		return fmt.Errorf("clipboard: send secret to clipboard holder: %w", writeErr)
	}

	return waitForReady(readyR)
}

// buildHolderCommand constructs (but does not start) the re-exec command
// a holder process is spawned with. Split out from spawnHolder so tests
// can assert its shape without actually forking.
func buildHolderCommand(self string, kind backendKind, secretR, readyW, devNull *os.File) *exec.Cmd {
	backendArg := "x11"
	if kind == backendWayland {
		backendArg = "wayland"
	}
	// CommandContext with a background context: the holder is meant to
	// outlive this process (that's the whole point), so there's no
	// request-scoped lifetime to tie it to - context.Background() is
	// never canceled, matching plain exec.Command's behavior.
	//nolint:gosec // G204: self comes from os.Executable (this program's own binary), not user input
	cmd := exec.CommandContext(context.Background(), self, holderSentinel, backendArg)
	cmd.ExtraFiles = []*os.File{secretR, readyW} // inherited as fd 3 and fd 4 in the child
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devNull, devNull, devNull
	// Setsid detaches the holder into its own session so it survives the
	// parent (CLI command or REPL) exiting, with no controlling terminal.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}

// waitForReady blocks until readyR yields a byte (ownership confirmed),
// is closed without one (the holder exited/failed first), or
// holderReadyTimeout elapses. It always closes readyR before returning.
func waitForReady(readyR *os.File) error {
	defer func() { _ = readyR.Close() }()

	ok := make(chan bool, 1)
	go func() {
		buf := make([]byte, 1)
		n, _ := readyR.Read(buf)
		ok <- n > 0
	}()

	select {
	case ready := <-ok:
		if ready {
			return nil
		}
		return errors.New("clipboard: clipboard holder exited before taking ownership")
	case <-time.After(holderReadyTimeout):
		return errors.New("clipboard: timed out waiting for clipboard holder to take ownership")
	}
}
