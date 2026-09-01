//go:build darwin

package clipboard

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

var (
	nsPasteboardClass       objc.Class
	nsStringClass           objc.Class
	generalPasteboardSel    objc.SEL
	clearContentsSel        objc.SEL
	setStringForTypeSel     objc.SEL
	stringWithUTF8StringSel objc.SEL
	nsPasteboardTypeString  objc.ID

	initErr error
)

func init() {
	appkit, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_GLOBAL|purego.RTLD_NOW)
	if err != nil {
		initErr = fmt.Errorf("clipboard: load AppKit.framework: %w", err)
		return
	}

	// NSPasteboardTypeString is an exported NSString* constant, not a
	// method - dlsym gives the address of the pointer variable itself,
	// so it must be dereferenced once to get the actual object.
	sym, err := purego.Dlsym(appkit, "NSPasteboardTypeString")
	if err != nil {
		initErr = fmt.Errorf("clipboard: resolve NSPasteboardTypeString: %w", err)
		return
	}
	// circumvent go vet's unsafeptr check (same idiom purego/objc itself
	// uses): reinterpret the address as unsafe.Pointer via its own
	// address-of rather than a direct uintptr->unsafe.Pointer conversion.
	nsPasteboardTypeString = *(*objc.ID)(*(*unsafe.Pointer)(unsafe.Pointer(&sym))) //nolint:gosec // G103: required to dereference the dlsym'd address of an AppKit constant

	nsPasteboardClass = objc.GetClass("NSPasteboard")
	nsStringClass = objc.GetClass("NSString")
	generalPasteboardSel = objc.RegisterName("generalPasteboard")
	clearContentsSel = objc.RegisterName("clearContents")
	setStringForTypeSel = objc.RegisterName("setString:forType:")
	stringWithUTF8StringSel = objc.RegisterName("stringWithUTF8String:")
}

// Write copies text to the system clipboard via AppKit's NSPasteboard,
// invoked directly through the Objective-C runtime (purego/objc) - no
// cgo, no external process. No helper process either: like Windows,
// macOS's pasteboard is an OS service that copies data at set-time
// rather than a selection an owner process must keep serving.
func Write(text string) error {
	if initErr != nil {
		return initErr
	}

	pasteboard := objc.ID(nsPasteboardClass).Send(generalPasteboardSel)
	if pasteboard == 0 {
		return errors.New("clipboard: NSPasteboard generalPasteboard returned nil")
	}
	pasteboard.Send(clearContentsSel)

	nsText := objc.ID(nsStringClass).Send(stringWithUTF8StringSel, text)
	if nsText == 0 {
		return errors.New("clipboard: failed to create NSString from text")
	}

	if ok := objc.Send[bool](pasteboard, setStringForTypeSel, nsText, nsPasteboardTypeString); !ok {
		return errors.New("clipboard: NSPasteboard setString:forType: failed")
	}
	return nil
}
