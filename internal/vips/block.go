package vips

/*
#cgo pkg-config: vips
#include <stdlib.h>
#include <vips/vips.h>
*/
import "C"
import "unsafe"

// blockOperation blocks a libvips operation class and all subclasses. libvips
// intentionally treats unknown names as no-ops, which lets one config span
// deployments with slightly different optional loader support.
func blockOperation(name string) {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	C.vips_operation_block_set(cname, C.gboolean(1))
}
