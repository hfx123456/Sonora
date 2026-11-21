package app

import "syscall"

var (
	fusionUser32           = syscall.NewLazyDLL("user32.dll")
	procFusionGetAsyncKey  = fusionUser32.NewProc("GetAsyncKeyState")
)

const vkLButton = 0x01

func fusionPrimaryButtonDown() bool {
	state, _, _ := procFusionGetAsyncKey.Call(vkLButton)
	return state&0x8000 != 0
}
