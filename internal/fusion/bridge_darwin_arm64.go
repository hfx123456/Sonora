//go:build darwin && arm64

package fusion

import _ "embed"

//go:embed bridge-darwin-arm64.bin
var bridge []byte

const bridgeFilename = "MySpotify.Lyrics"
