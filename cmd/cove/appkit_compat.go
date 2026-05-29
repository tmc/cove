package main

import (
	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/objc"
)

const nsEventTypeApplicationDefined = appkit.NSEventType(15)
const nsEventMaskAny = appkit.NSEventMask(-1) // NSAnyEventMask is NSUIntegerMax.
const nsToolbarDisplayModeIconOnly = appkit.NSToolbarDisplayMode(2)
const nsBoxCustom = appkit.NSBoxType(4)
const nsAlertStyleWarning = appkit.NSAlertStyle(0)
const nsBezelStyleDisclosure = appkit.NSBezelStylePushDisclosure

func addSubview(parent, child appkit.NSView) {
	if parent.ID == 0 || child.ID == 0 {
		return
	}
	objc.Send[struct{}](parent.ID, objc.Sel("addSubview:"), child)
}

func bitmapImageRepForCachingDisplay(view appkit.NSView, rect corefoundation.CGRect) appkit.NSBitmapImageRep {
	if view.ID == 0 {
		return appkit.NSBitmapImageRep{}
	}
	repID := objc.Send[objc.ID](view.ID, objc.Sel("bitmapImageRepForCachingDisplayInRect:"), rect)
	return appkit.NSBitmapImageRepFromID(repID)
}

func cacheDisplayInRectToBitmapImageRep(view appkit.NSView, rect corefoundation.CGRect, rep appkit.NSBitmapImageRep) {
	if view.ID == 0 || rep.ID == 0 {
		return
	}
	objc.Send[struct{}](view.ID, objc.Sel("cacheDisplayInRect:toBitmapImageRep:"), rect, rep)
}
