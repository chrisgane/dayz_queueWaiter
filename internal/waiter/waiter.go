// Package waiter keeps trying to join a full server: double-click it, press
// Enter on the "server full" popup, wait a moment, and go again, until the
// game leaves the server browser.
package waiter

import (
	"errors"
	"math/rand/v2"
	"time"

	"github.com/chrisgane/dayz_queueWaiter/internal/detect"
)

// Game is the running game, as seen and driven from outside.
type Game interface {
	// Observe looks at the screen.
	Observe() (detect.State, error)
	// DoubleClick double-clicks the chosen server.
	DoubleClick() error
	// PressEnter presses Enter, which closes a popup.
	PressEnter() error
	// InFront reports whether DayZ is the active window. Nothing is clicked
	// or pressed while it isn't.
	InFront() bool
	// MouseMoved reports whether the player has moved the mouse away from
	// the server since it was chosen.
	MouseMoved() bool
}

// Clock is time.Now and time.Sleep, replaceable in tests.
type Clock interface {
	Now() time.Time
	// Sleep waits for d. It returns false straight away if stop is closed.
	Sleep(d time.Duration, stop <-chan struct{}) bool
}

// Outcome is how a run ended.
type Outcome int

const (
	// Joined: the game left the server browser, so it's connecting or
	// queueing.
	Joined Outcome = iota
	// GaveUp: Settings.GiveUp passed without getting in.
	GaveUp
	// Stopped: the stop channel was closed.
	Stopped
	// MouseMoved: the player moved the mouse.
	MouseMoved
	// Failed: something went wrong; Run also returns an error.
	Failed
)

// Settings are the timings the player can change.
type Settings struct {
	// MinGap and MaxGap bound the random pause between the popup closing
	// and the next double-click.
	MinGap, MaxGap time.Duration
	// GiveUp is how long to keep trying.
	GiveUp time.Duration
}

// Fixed timings.
const (
	pollMin, pollMax = 80 * time.Millisecond, 130 * time.Millisecond
	// The screen must stay away from the server browser this long to count
	// as joined, so a brief flicker doesn't stop the run.
	confirmAway = 3 * time.Second
	// How long to "notice" the server full popup before pressing Enter.
	reactMin, reactMax = 250 * time.Millisecond, 550 * time.Millisecond
	// An unknown popup is left this long before pressing Enter, in case it
	// goes away by itself.
	otherPopupWait = 3 * time.Second
	// Press Enter again if the popup is still there after this long.
	enterRetry = 1500 * time.Millisecond
	maxEnters  = 5
	// If nothing happens this long after a double-click, try again.
	noAnswer = 12 * time.Second
	// After the server query sees a place open, try again after this long
	// instead of the usual gap.
	nudgeMin, nudgeMax = 150 * time.Millisecond, 400 * time.Millisecond
	// The first try after starting.
	firstMin, firstMax = 300 * time.Millisecond, 600 * time.Millisecond
)

// Runner runs one round of trying to join.
type Runner struct {
	Game  Game
	Clock Clock
	Settings
	Rand *rand.Rand
	Logf func(format string, args ...any)
	// SlotOpened, if set, signals that the server query saw a place open.
	// The next try then comes sooner.
	SlotOpened <-chan struct{}

	// Tries counts double-clicks.
	Tries int
}

type phase int

const (
	ready   phase = iota // waiting for nextTry, then double-click
	waiting              // double-clicked, waiting for the result
	closing              // pressed Enter on a popup, waiting for it to close
)

// ErrPopupStuck means a popup didn't close after pressing Enter several
// times.
var ErrPopupStuck = errors.New("the popup didn't close after pressing Enter")

// Run keeps trying until the game leaves the server browser, time runs out,
// the player moves the mouse, or stop is closed.
func (r *Runner) Run(stop <-chan struct{}) (Outcome, error) {
	now := r.Clock.Now()
	deadline := now.Add(r.GiveUp)
	ph := ready
	nextTry := now.Add(r.between(firstMin, firstMax))
	var triedAt, awaySince, enterAt time.Time
	var lastState detect.State = -1
	enters := 0
	paused := false

	for {
		if !r.Clock.Sleep(r.between(pollMin, pollMax), stop) {
			return Stopped, nil
		}
		now = r.Clock.Now()
		if !now.Before(deadline) {
			return GaveUp, nil
		}
		if !r.Game.InFront() {
			if !paused {
				r.logf("Paused: DayZ isn't the active window.")
				paused = true
			}
			awaySince = time.Time{}
			continue
		}
		if paused {
			r.logf("DayZ is back in front, carrying on.")
			paused = false
		}
		st, err := r.Game.Observe()
		if err != nil {
			return Failed, err
		}
		// The game may move the mouse itself once it leaves the server
		// browser, so only check on the browser.
		if st != detect.Away && r.Game.MouseMoved() {
			return MouseMoved, nil
		}
		prev := lastState
		lastState = st

		if st == detect.Away {
			if awaySince.IsZero() {
				awaySince = now
			}
			if now.Sub(awaySince) >= confirmAway {
				return Joined, nil
			}
			continue
		}
		awaySince = time.Time{}

		if st.IsPopup() {
			if !prev.IsPopup() || (st == detect.PopupFull && prev == detect.PopupOther) {
				// A popup appeared: press Enter after a moment.
				if st == detect.PopupFull {
					if ph == waiting {
						r.logf("Try %d: server full.", r.Tries)
					}
					enterAt = now.Add(r.between(reactMin, reactMax))
				} else {
					r.logf("A popup I don't recognise is showing; closing it in a few seconds.")
					enterAt = now.Add(otherPopupWait)
				}
			}
			if now.Before(enterAt) {
				continue
			}
			if enters >= maxEnters {
				return Failed, ErrPopupStuck
			}
			if err := r.Game.PressEnter(); err != nil {
				return Failed, err
			}
			enters++
			ph = closing
			enterAt = now.Add(enterRetry)
			continue
		}

		// The server browser, no popup.
		if ph == closing {
			ph = ready
			enters = 0
			nextTry = now.Add(r.between(r.MinGap, r.MaxGap))
		}
		if ph == waiting && now.Sub(triedAt) >= noAnswer {
			r.logf("Try %d: no answer, trying again.", r.Tries)
			ph = ready
			nextTry = now
		}
		if ph == ready && r.slotOpened() {
			if soon := now.Add(r.between(nudgeMin, nudgeMax)); soon.Before(nextTry) {
				nextTry = soon
			}
		}
		if ph == ready && !now.Before(nextTry) {
			if err := r.Game.DoubleClick(); err != nil {
				return Failed, err
			}
			r.Tries++
			triedAt = now
			ph = waiting
		}
	}
}

func (r *Runner) slotOpened() bool {
	select {
	case <-r.SlotOpened:
		return true
	default:
		return false
	}
}

// between picks a random duration from lo to hi.
func (r *Runner) between(lo, hi time.Duration) time.Duration {
	if hi <= lo {
		return lo
	}
	return lo + time.Duration(r.Rand.Int64N(int64(hi-lo)+1))
}

func (r *Runner) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
	}
}

// RealClock is the wall clock.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) Sleep(d time.Duration, stop <-chan struct{}) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-stop:
		return false
	}
}
