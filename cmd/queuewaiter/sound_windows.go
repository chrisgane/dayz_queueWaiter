package main

import (
	"time"

	"golang.org/x/sys/windows"
)

var pBeep = windows.NewLazySystemDLL("kernel32.dll").NewProc("Beep")

type note struct {
	hz, ms int
}

func play(notes ...note) {
	for _, n := range notes {
		if n.hz == 0 {
			time.Sleep(time.Duration(n.ms) * time.Millisecond)
			continue
		}
		pBeep.Call(uintptr(n.hz), uintptr(n.ms))
	}
}

func soundStarted() { go play(note{880, 120}, note{0, 40}, note{1320, 120}) }

func soundStopped() { go play(note{660, 150}, note{0, 40}, note{440, 200}) }

func soundError() { go play(note{220, 400}) }

func soundGaveUp() { go play(note{784, 200}, note{659, 200}, note{523, 200}, note{392, 500}) }

// soundJoined repeats so it's hard to miss.
func soundJoined() {
	go func() {
		for range 3 {
			play(note{523, 150}, note{659, 150}, note{784, 150}, note{1047, 400}, note{0, 500})
		}
	}()
}
