//go:build windows

package fusion

import _ "embed"

//go:embed bridge.bin
var bridge []byte

const bridgeFilename = "MySpotify.Lyrics.exe"
