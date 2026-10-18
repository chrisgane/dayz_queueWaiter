// Queue Waiter keeps trying to join a full DayZ server from the in-game
// server browser until it gets in.
//
//	queuewaiter.exe [-server IP:port] [-min 1s] [-max 2.5s] [-giveup 30m]
//
// In DayZ's server browser, hover over the server and press F8. It
// double-clicks the server, presses Enter on the "server full" popup, waits
// a moment and goes again, until the game leaves the server browser.
// F8 again, or moving the mouse, stops it.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"log"
	"math/rand/v2"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/chrisgane/dayz_queueWaiter/internal/a2s"
	"github.com/chrisgane/dayz_queueWaiter/internal/detect"
	"github.com/chrisgane/dayz_queueWaiter/internal/waiter"
)

const (
	// The mouse counts as moved if it's this many pixels from the server.
	mouseSlack = 10
)

var logger = log.New(os.Stdout, "", log.Ltime)

func main() {
	server := flag.String("server", "54.38.209.2:2302", "the server's IP:port, as shown in DayZ's server browser")
	queryPort := flag.Int("query", 0, "the server's query port (0: ask Steam)")
	minGap := flag.Duration("min", time.Second, "shortest pause between tries")
	maxGap := flag.Duration("max", 2500*time.Millisecond, "longest pause between tries")
	giveUp := flag.Duration("giveup", 30*time.Minute, "stop trying after this long")
	flag.Parse()
	if *minGap <= 0 || *maxGap < *minGap || *giveUp <= 0 {
		fatal(errors.New("-min must be above 0, -max at least -min, and -giveup above 0"))
	}

	dpiAware()
	setTitle("DayZ Queue Waiter")
	fmt.Println("DayZ Queue Waiter")
	fmt.Println()
	fmt.Println("  1. In DayZ's server browser, hover the mouse over the server.")
	fmt.Println("  2. Press F8. It keeps trying to join until you get in.")
	fmt.Println("  3. Press F8 again, or move the mouse, to stop.")
	fmt.Println()
	fmt.Println("  If F8 does nothing, press Enter in this window instead, then within")
	fmt.Println("  5 seconds switch to DayZ and hover over the server.")
	fmt.Println()
	fmt.Println("  F7 saves a screenshot of what this app sees, for troubleshooting.")
	fmt.Println()
	fmt.Printf("Server %s. Tries every %v-%v, for up to %v.\n\n", *server, *minGap, *maxGap, *giveUp)

	keys := make(chan key, 4)
	go watchKeys(keys)
	go watchConsole(keys)
	soundReady()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	slot := make(chan struct{}, 1)
	go watchServer(ctx, *server, *queryPort, slot)

	settings := waiter.Settings{MinGap: *minGap, MaxGap: *maxGap, GiveUp: *giveUp}
	var stop chan struct{} // set while a run is going
	stopping, counting := false, false
	done := make(chan struct{})
	for {
		select {
		case <-ctx.Done():
			if stop != nil {
				if !stopping {
					close(stop)
				}
				<-done
			}
			return
		case k := <-keys:
			if k == keyF8 {
				logger.Println("F8 pressed.")
			}
			begin := false
			switch {
			case k == keyShot:
				logger.Println("F7 pressed.")
				saveShot()
			case k == keyCountdown:
				// Ignored if F8 started a run during the countdown.
				begin = counting && stop == nil
				counting = false
			case stop != nil:
				if !stopping {
					logger.Println("Stopping.")
					close(stop)
					stopping = true
				}
			case k == keyConsole:
				if !counting {
					counting = true
					logger.Println("Starting in 5 seconds: switch to DayZ and hover over the server.")
					go countdown(keys)
				}
			case k == keyF8:
				begin = true
			}
			if begin {
				// Drop a stale "place opened" from before this run.
				select {
				case <-slot:
				default:
				}
				s := make(chan struct{})
				if start(settings, slot, s, done) {
					stop = s
				}
			}
		case <-done:
			stop, stopping = nil, false
		}
	}
}

// start checks DayZ is showing the server browser under the mouse and, if
// so, starts trying in the background. done is signalled when it ends.
func start(settings waiter.Settings, slot <-chan struct{}, stop chan struct{}, done chan<- struct{}) bool {
	at := cursorPos()
	if !dayzInFront() {
		logger.Println("DayZ isn't the active window. Open its server browser, hover over the server, then press F8.")
		soundError()
		return false
	}
	capture, err := openCapture(at.X, at.Y)
	if err != nil {
		logger.Println("Can't read the screen:", err)
		soundError()
		return false
	}
	frame, err := capture.Grab()
	if err != nil {
		capture.Close()
		logger.Println("Can't read the screen:", err)
		soundError()
		return false
	}
	st := detect.Classify(frame)
	mon, _ := monitorAt(at.X, at.Y)
	switch {
	case st == detect.Away && detect.Brightness(frame) < 3:
		logger.Printf("The screen reads as black (using %s).", capture.Method())
		logger.Println("Fullscreen DayZ is hiding the picture. Set Display mode to Borderless in DayZ's video options and try again.")
		save(frame, st)
	case st == detect.Away:
		logger.Println("I can't see DayZ's server browser on this screen.")
		save(frame, st)
	case st == detect.Browser && !detect.ServerList.Contains(frame.W, frame.H, int(at.X-mon.Left), int(at.Y-mon.Top)):
		logger.Println("The mouse isn't over the server list. Hover over the server, then press F8.")
	default:
		go run(&game{capture: capture, at: at}, settings, slot, stop, done)
		return true
	}
	capture.Close()
	soundError()
	return false
}

func run(g *game, settings waiter.Settings, slot <-chan struct{}, stop chan struct{}, done chan<- struct{}) {
	defer func() { done <- struct{}{} }()
	defer g.capture.Close()
	logger.Printf("Started. Trying the server under the mouse (%d, %d). Press F8 to stop.", g.at.X, g.at.Y)
	soundStarted()
	r := &waiter.Runner{
		Game:       g,
		Clock:      waiter.RealClock{},
		Settings:   settings,
		Rand:       rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())),
		Logf:       func(format string, args ...any) { logger.Printf(format, args...) },
		SlotOpened: slot,
	}
	began := time.Now()
	out, err := r.Run(stop)
	took := time.Since(began).Round(time.Second)
	switch out {
	case waiter.Joined:
		logger.Printf("You're in! DayZ left the server browser after %d tries (%v).", r.Tries, took)
		soundJoined()
	case waiter.GaveUp:
		logger.Printf("Gave up after %v (%d tries).", took, r.Tries)
		soundGaveUp()
	case waiter.Stopped:
		logger.Printf("Stopped after %d tries.", r.Tries)
		soundStopped()
	case waiter.MouseMoved:
		logger.Printf("Stopped because the mouse moved, after %d tries.", r.Tries)
		soundStopped()
	case waiter.Failed:
		logger.Printf("Stopped: %v", err)
		soundError()
	}
	fmt.Println()
}

// game is DayZ, seen through screen capture and driven with the mouse and
// keyboard.
type game struct {
	capture capturer
	at      point
}

func (g *game) Observe() (detect.State, error) {
	f, err := g.capture.Grab()
	if err != nil {
		return detect.Away, err
	}
	return detect.Classify(f), nil
}

func (g *game) DoubleClick() error {
	doubleClick(g.at)
	return nil
}

func (g *game) PressEnter() error {
	pressEnter()
	return nil
}

func (g *game) InFront() bool { return dayzInFront() }

func (g *game) MouseMoved() bool {
	p := cursorPos()
	return abs(p.X-g.at.X) > mouseSlack || abs(p.Y-g.at.Y) > mouseSlack
}

func abs(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// watchServer shows how full the server is, and signals slot when a place
// seems to open up.
func watchServer(ctx context.Context, server string, port int, slot chan<- struct{}) {
	host, gamePort, err := net.SplitHostPort(server)
	gp, perr := strconv.Atoi(gamePort)
	if err != nil || perr != nil {
		logger.Printf("Can't watch the server: %q isn't IP:port.", server)
		return
	}
	if port == 0 {
		lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		port, err = a2s.LookupQueryPort(lctx, host, gp)
		cancel()
		if err != nil {
			port = 27016
			logger.Printf("Couldn't ask Steam for the query port (%v); trying %d.", err, port)
		}
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	last, failing := "", false
	prevTotal := -1
	for {
		qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		info, err := a2s.Query(qctx, addr)
		cancel()
		switch {
		case err != nil && !failing:
			logger.Printf("Can't get the server's player count (%v). Still trying to join; this is only for information.", err)
			failing = true
		case err == nil:
			failing = false
			if last == "" {
				logger.Printf("Server: %s", info.Name)
			}
			if s := info.String(); s != last {
				logger.Printf("Server: %s", s)
				last = s
			}
			total := info.Players + max(info.Queue, 0)
			if prevTotal >= 0 && total < prevTotal {
				select {
				case slot <- struct{}{}:
				default:
				}
			}
			prevTotal = total
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2500*time.Millisecond + time.Duration(rand.Int64N(int64(time.Second)))):
		}
	}
}

// saveShot saves what the app sees on the monitor under the mouse.
func saveShot() {
	at := cursorPos()
	capture, err := openCapture(at.X, at.Y)
	if err != nil {
		logger.Println("Can't read the screen:", err)
		return
	}
	defer capture.Close()
	frame, err := capture.Grab()
	if err != nil {
		logger.Println("Can't read the screen:", err)
		return
	}
	st := detect.Classify(frame)
	logger.Printf("I see: %v (using %s).", st, capture.Method())
	save(frame, st)
}

func save(f *detect.BGRA, st detect.State) {
	img := image.NewRGBA(image.Rect(0, 0, f.W, f.H))
	for y := 0; y < f.H; y++ {
		for x := 0; x < f.W; x++ {
			r, g, b := f.RGB(x, y)
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = r, g, b, 255
		}
	}
	dir := "."
	if exe, err := os.Executable(); err == nil {
		dir = filepath.Dir(exe)
	}
	name := filepath.Join(dir, "queuewaiter-screenshot.png")
	file, err := os.Create(name)
	if err == nil {
		err = png.Encode(file, img)
		if cerr := file.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		logger.Println("Couldn't save the screenshot:", err)
		return
	}
	logger.Printf("Saved what I see (%v) to %s", st, name)
}

func setTitle(s string) {
	p, _ := windows.UTF16PtrFromString(s)
	windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleTitleW").Call(uintptr(unsafe.Pointer(p)))
}

func fatal(err error) {
	fmt.Println("Error:", err)
	fmt.Println("Press Enter to close.")
	fmt.Scanln()
	os.Exit(1)
}
