package main

import (
	"bufio"
	"os"
	"time"
)

// key is something the player pressed.
type key int

const (
	keyF8        key = iota // start / stop
	keyShot                 // F7: save what the app sees
	keyConsole              // Enter in the console window
	keyCountdown            // the countdown after keyConsole finished
)

var pGetAsyncKeyState = user32.NewProc("GetAsyncKeyState")

const vkF7, vkF8 = 0x76, 0x77

// watchKeys watches F7 and F8 by checking the keyboard every 25ms, the way
// OBS does its hotkeys. Windows' own hotkeys (RegisterHotKey) don't fire
// while a game that turns them off, like DayZ, is in front.
func watchKeys(out chan<- key) {
	keys := []struct {
		vk   uintptr
		send key
		down bool
	}{{vkF8, keyF8, false}, {vkF7, keyShot, false}}
	for range time.Tick(25 * time.Millisecond) {
		for i := range keys {
			r, _, _ := pGetAsyncKeyState.Call(keys[i].vk)
			down := r&0x8000 != 0
			if down && !keys[i].down {
				select {
				case out <- keys[i].send:
				default:
				}
			}
			keys[i].down = down
		}
	}
}

// watchConsole sends keyConsole each time Enter is pressed in the console.
func watchConsole(out chan<- key) {
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		out <- keyConsole
	}
}

// countdown beeps for 5 seconds, then sends keyCountdown.
func countdown(out chan<- key) {
	for range 4 {
		soundTick()
		time.Sleep(time.Second)
	}
	time.Sleep(time.Second)
	out <- keyCountdown
}
