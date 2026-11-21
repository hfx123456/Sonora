//go:build darwin && amd64

package fusion

import _ "embed"

//go:embed bridge-darwin-amd64.bin
var bridge []byte

const bridgeFilename = "MySpotify.Lyrics"
