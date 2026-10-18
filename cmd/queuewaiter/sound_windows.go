package main

import (
	"bytes"
	"encoding/binary"
	"math"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Sounds are generated as WAV in memory and played through the normal
// sound output. (The older Beep function is silent on some PCs.)

var (
	pPlaySoundW = windows.NewLazySystemDLL("winmm.dll").NewProc("PlaySoundW")
	soundMu     sync.Mutex
)

type note struct {
	hz, ms int
}

const sampleRate = 22050

// wav renders notes as a 16-bit mono WAV file.
func wav(notes ...note) []byte {
	var pcm []int16
	for _, n := range notes {
		count := sampleRate * n.ms / 1000
		fade := min(count/2, sampleRate/200) // 5ms fade in and out, so no clicks
		for i := range count {
			v := 0.0
			if n.hz > 0 {
				v = math.Sin(2 * math.Pi * float64(n.hz) * float64(i) / sampleRate)
				if i < fade {
					v *= float64(i) / float64(fade)
				} else if count-i < fade {
					v *= float64(count-i) / float64(fade)
				}
			}
			pcm = append(pcm, int16(v*0.5*math.MaxInt16))
		}
	}
	var b bytes.Buffer
	size := len(pcm) * 2
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+size))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(sampleRate), uint32(sampleRate * 2), uint16(2), uint16(16)} {
		binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(size))
	binary.Write(&b, binary.LittleEndian, pcm)
	return b.Bytes()
}

// play plays the notes in the background.
func play(notes ...note) {
	data := wav(notes...)
	go func() {
		soundMu.Lock()
		defer soundMu.Unlock()
		const sndSync, sndMemory, sndNoDefault = 0x0000, 0x0004, 0x0002
		pPlaySoundW.Call(uintptr(unsafe.Pointer(&data[0])), 0, sndSync|sndMemory|sndNoDefault)
	}()
}

func soundReady() { play(note{660, 80}) }

func soundTick() { play(note{1000, 60}) }

func soundStarted() { play(note{880, 120}, note{0, 40}, note{1320, 120}) }

func soundStopped() { play(note{660, 150}, note{0, 40}, note{440, 200}) }

func soundError() { play(note{220, 400}) }

func soundGaveUp() { play(note{784, 200}, note{659, 200}, note{523, 200}, note{392, 500}) }

// soundJoined repeats so it's hard to miss.
func soundJoined() {
	var tune []note
	for range 3 {
		tune = append(tune, note{523, 150}, note{659, 150}, note{784, 150}, note{1047, 400}, note{0, 500})
	}
	play(tune...)
}
