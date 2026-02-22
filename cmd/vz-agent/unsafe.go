package main

import "unsafe"

func unsafePointer(p *byte) unsafe.Pointer {
	return unsafe.Pointer(p)
}
