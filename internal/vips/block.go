package vips

/*
#cgo pkg-config: vips
#include <stdlib.h>
#include <vips/vips.h>
*/
import "C"
import "unsafe"

// setOperationBlocked blocks or unblocks a libvips operation class and all
// subclasses. libvips intentionally treats unknown names as no-ops, which lets
// one config span deployments with slightly different optional loader support.
func setOperationBlocked(name string, blocked bool) {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	var value C.gboolean
	if blocked {
		value = C.gboolean(1)
	}
	C.vips_operation_block_set(cname, value)
}

func blockOperation(name string) { setOperationBlocked(name, true) }

func unblockOperation(name string) { setOperationBlocked(name, false) }
