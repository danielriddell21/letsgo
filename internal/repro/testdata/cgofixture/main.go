// Command cgofixture proves that a cgo build is byte-identical across
// machines, which is the claim that makes cgo support worth having.
//
// It is separate from the pure-Go fixture rather than a flag on it: the two
// answer different questions, and a single fixture that sometimes used cgo
// would make a failure ambiguous between the Go compiler and the C one.
package main

/*
#include <stdlib.h>
#include "checksum.h"
*/
import "C"

import (
	"fmt"
	"os"
	"unsafe"
)

var version = "dev"

func main() {
	arg := "letsgo"
	if len(os.Args) > 1 {
		arg = os.Args[1]
	}

	text := C.CString(arg)
	defer C.free(unsafe.Pointer(text))

	fmt.Printf("cgofixture %s: %d\n", version, uint64(C.checksum(text)))
}
