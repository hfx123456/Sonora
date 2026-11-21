//go:build !windows && !(darwin && (arm64 || amd64))

package fusion

var bridge []byte

const bridgeFilename = "MySpotify.Lyrics"
