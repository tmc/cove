package diskimages2

import (
	"fmt"
	"unsafe"

	di2 "github.com/tmc/appledocs/generated/diskimages2"
	"github.com/tmc/appledocs/generated/foundation"
	"github.com/tmc/appledocs/generated/objc"
)

// RetrieveInfo returns metadata about a disk image file using DIImageInfoParams.
// The returned ImageInfo.Raw contains the NSDictionary keys as strings.
func RetrieveInfo(path string) (*ImageInfo, error) {
	if err := ensureLoaded(); err != nil {
		return nil, err
	}

	url := foundation.FileURL(path)

	params, err := di2.NewDIImageInfoParamsWithURLError(url)
	if err != nil {
		return nil, fmt.Errorf("diskimages2: init info params: %w", err)
	}
	if params.ID == 0 {
		return nil, fmt.Errorf("diskimages2: DIImageInfoParams init failed")
	}

	if _, err := params.RetrieveWithError(); err != nil {
		return nil, fmt.Errorf("diskimages2: retrieve info: %w", err)
	}

	dictIface := params.ImageInfo()
	info := &ImageInfo{Raw: make(map[string]string)}
	if dictIface != nil && dictIface.GetID() != 0 {
		dict := foundation.NSDictionaryFrom(dictIface.GetID())
		keys := dict.AllKeys()
		for _, key := range keys {
			val := dict.ObjectForKey(key)
			if val == nil {
				continue
			}
			keyStr := foundation.NSStringFrom(key.GetID()).String()
			valStr := foundation.NSStringFrom(
				objc.Send[objc.ID](val.GetID(), objc.Sel("description")),
			).String()
			info.Raw[keyStr] = valStr
		}
	}
	return info, nil
}

// ListAttached returns info about all currently attached disk images.
func ListAttached() ([]AttachedDeviceInfo, error) {
	if err := ensureLoaded(); err != nil {
		return nil, err
	}

	cls := objc.ID(objc.GetClass("DIAttachedDeviceInfo"))
	if cls == 0 {
		return nil, fmt.Errorf("diskimages2: DIAttachedDeviceInfo class not available")
	}

	var errPtr objc.ID
	arrayID := objc.Send[objc.ID](
		cls,
		objc.Sel("newDevicesArrayWithError:"),
		unsafe.Pointer(&errPtr),
	)
	if arrayID == 0 {
		if errPtr != 0 {
			nsErr := foundation.NSErrorFrom(errPtr)
			return nil, fmt.Errorf("diskimages2: list attached: %s", nsErr.LocalizedDescription())
		}
		return nil, nil
	}

	count := objc.Send[uint](arrayID, objc.Sel("count"))
	result := make([]AttachedDeviceInfo, 0, count)
	for i := uint(0); i < count; i++ {
		item := objc.Send[objc.ID](arrayID, objc.Sel("objectAtIndex:"), i)
		if item == 0 {
			continue
		}
		info := AttachedDeviceInfo{
			BSDName: foundation.NSStringFrom(
				objc.Send[objc.ID](item, objc.Sel("BSDName")),
			).String(),
		}
		urlID := objc.Send[objc.ID](item, objc.Sel("imageURL"))
		if urlID != 0 {
			info.ImageURL = foundation.NSStringFrom(
				objc.Send[objc.ID](urlID, objc.Sel("path")),
			).String()
		}
		info.MediaSize = objc.Send[int64](item, objc.Sel("mediaSize"))
		result = append(result, info)
	}
	return result, nil
}
