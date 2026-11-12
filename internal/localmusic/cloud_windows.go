package localmusic

import (
	"io/fs"
	"syscall"
)

func cloudOnly(info fs.FileInfo) bool {
	attributes, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return ok && attributes.FileAttributes&(0x1000|0x40000|0x400000) != 0
}
