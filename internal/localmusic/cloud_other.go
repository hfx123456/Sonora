//go:build !windows

package localmusic

import "io/fs"

func cloudOnly(info fs.FileInfo) bool { return false }
