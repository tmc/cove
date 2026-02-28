package diskimages2

import (
	"fmt"
	"unsafe"

	di2 "github.com/tmc/apple/private/diskimages2"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
)

// CreateASIF creates an Apple Sparse Image Format disk image at the given path.
// ASIF only allocates blocks on write (similar to QCOW2), making it efficient
// for VM disk images.
//
// sizeBytes is the virtual size of the image. The on-disk size starts near zero
// and grows as data is written.
func CreateASIF(path string, sizeBytes int64) error {
	if err := ensureLoaded(); err != nil {
		return err
	}

	url := foundation.NewURLFileURLWithPath(path)
	numBlocks := uint64(sizeBytes / 512)

	params, err := di2.NewDICreateASIFParamsWithURLNumBlocksError(url, numBlocks)
	if err != nil {
		return fmt.Errorf("diskimages2: init create params: %w", err)
	}
	if params.ID == 0 {
		return fmt.Errorf("diskimages2: DICreateASIFParams init failed")
	}

	// Call [DiskImages2 createBlankWithParams:error:] via the facade class.
	cls := objc.ID(objc.GetClass("DiskImages2"))
	var errPtr objc.ID
	ok := objc.Send[bool](
		cls,
		objc.Sel("createBlankWithParams:error:"),
		params, unsafe.Pointer(&errPtr),
	)
	if !ok {
		if errPtr != 0 {
			nsErr := foundation.NSErrorFromID(errPtr)
			return fmt.Errorf("diskimages2: create: %s", nsErr.LocalizedDescription())
		}
		return fmt.Errorf("diskimages2: createBlankWithParams failed")
	}
	return nil
}

// Resize resizes an existing disk image (ASIF or other supported format).
// newSizeBytes is the new virtual size.
func Resize(path string, newSizeBytes int64) error {
	if err := ensureLoaded(); err != nil {
		return err
	}

	url := foundation.NewURLFileURLWithPath(path)

	params, err := di2.NewDIResizeParamsWithURLSizeError(url, uint64(newSizeBytes))
	if err != nil {
		return fmt.Errorf("diskimages2: init resize params: %w", err)
	}
	if params.ID == 0 {
		return fmt.Errorf("diskimages2: DIResizeParams init failed")
	}

	if _, err := params.ResizeWithError(); err != nil {
		return fmt.Errorf("diskimages2: resize: %w", err)
	}
	return nil
}
