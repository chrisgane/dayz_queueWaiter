package waiter

import (
	"errors"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/chrisgane/dayz_queueWaiter/internal/detect"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }

func (c *fakeClock) Sleep(d time.Duration, stop <-chan struct{}) bool {
	select {
	case <-stop:
		return false
	default:
	}
	c.t = c.t.Add(d)
	return true
}

// sim is a pretend DayZ: a double-click shows the server full popup after a
// short connect, until try number joinOn, which goes to the queue screen.
type sim struct {
	clock *fakeClock
	state detect.State
	// change, at changeAt, switches to next.
	changeAt time.Time
	next     detect.State

	joinOn     int // 0: never
	popup      detect.State
	stuckPopup bool
	notInFront func(time.Time) bool
	moved      bool

	clicks  []time.Time
	enters  []time.Time
	actions []string // click/enter, in order
}

func newSim(c *fakeClock) *sim {
	return &sim{clock: c, state: detect.Browser, popup: detect.PopupFull}
}

func (s *sim) Observe() (detect.State, error) {
	if !s.changeAt.IsZero() && !s.clock.t.Before(s.changeAt) {
		s.state, s.changeAt = s.next, time.Time{}
	}
	return s.state, nil
}

func (s *sim) after(d time.Duration, st detect.State) {
	s.changeAt, s.next = s.clock.t.Add(d), st
}

func (s *sim) DoubleClick() error {
	if s.state != detect.Browser {
		return errors.New("double-clicked while not on the server browser")
	}
	s.clicks = append(s.clicks, s.clock.t)
	s.actions = append(s.actions, "click")
	if s.joinOn > 0 && len(s.clicks) == s.joinOn {
		s.after(500*time.Millisecond, detect.Away)
	} else {
		s.after(700*time.Millisecond, s.popup)
	}
	return nil
}

func (s *sim) PressEnter() error {
	s.enters = append(s.enters, s.clock.t)
	s.actions = append(s.actions, "enter")
	if !s.state.IsPopup() {
		return errors.New("pressed Enter with no popup")
	}
	if !s.stuckPopup {
		s.after(50*time.Millisecond, detect.Browser)
	}
	return nil
}

func (s *sim) InFront() bool {
	return s.notInFront == nil || !s.notInFront(s.clock.t)
}

func (s *sim) MouseMoved() bool { return s.moved }

var start = time.Date(2026, 10, 18, 20, 0, 0, 0, time.UTC)

func newRunner(g Game, c Clock) *Runner {
	return &Runner{
		Game:     g,
		Clock:    c,
		Settings: Settings{MinGap: time.Second, MaxGap: 2500 * time.Millisecond, GiveUp: 30 * time.Minute},
		Rand:     rand.New(rand.NewPCG(1, 2)),
		Logf:     func(string, ...any) {},
	}
}

func TestJoins(t *testing.T) {
	c := &fakeClock{start}
	g := newSim(c)
	g.joinOn = 20
	r := newRunner(g, c)
	out, err := r.Run(nil)
	if out != Joined || err != nil {
		t.Fatalf("got %v, %v", out, err)
	}
	if len(g.clicks) != 20 || len(g.enters) != 19 || r.Tries != 20 {
		t.Fatalf("%d clicks, %d enters, %d tries", len(g.clicks), len(g.enters), r.Tries)
	}
	for i, a := range g.actions {
		want := "click"
		if i%2 == 1 {
			want = "enter"
		}
		if a != want {
			t.Fatalf("action %d is %s, want %s", i, a, want)
		}
	}
	for i := range g.enters {
		// Enter comes after noticing the popup (shown 700ms after the click).
		react := g.enters[i].Sub(g.clicks[i]) - 700*time.Millisecond
		if react < reactMin || react > reactMax+2*pollMax {
			t.Errorf("try %d: pressed Enter %v after the popup", i+1, react)
		}
		// The popup closes 50ms after Enter, then the gap.
		gap := g.clicks[i+1].Sub(g.enters[i])
		if gap < r.MinGap || gap > 50*time.Millisecond+r.MaxGap+2*pollMax {
			t.Errorf("try %d: %v between Enter and the next try", i+1, gap)
		}
	}
	// Joined is only reported after the queue screen has stayed up.
	if d := c.t.Sub(g.clicks[19]); d < 500*time.Millisecond+confirmAway {
		t.Errorf("reported joined %v after the last click", d)
	}
}

func TestGivesUp(t *testing.T) {
	c := &fakeClock{start}
	g := newSim(c)
	r := newRunner(g, c)
	out, err := r.Run(nil)
	if out != GaveUp || err != nil {
		t.Fatalf("got %v, %v", out, err)
	}
	if c.t.Sub(start) < 30*time.Minute || c.t.Sub(start) > 30*time.Minute+time.Second {
		t.Errorf("gave up after %v", c.t.Sub(start))
	}
	// Roughly one try every 1.9s (connect 0.7 + react 0.4 + gap 1.75 on average).
	if n := len(g.clicks); n < 500 || n > 750 {
		t.Errorf("%d tries in 30 minutes", n)
	}
}

func TestStop(t *testing.T) {
	c := &fakeClock{start}
	stop := make(chan struct{})
	close(stop)
	out, err := newRunner(newSim(c), c).Run(stop)
	if out != Stopped || err != nil {
		t.Fatalf("got %v, %v", out, err)
	}
}

func TestPausesWhenNotInFront(t *testing.T) {
	c := &fakeClock{start}
	g := newSim(c)
	g.joinOn = 3
	away0, away1 := start.Add(5*time.Second), start.Add(65*time.Second)
	g.notInFront = func(t time.Time) bool { return !t.Before(away0) && t.Before(away1) }
	out, _ := newRunner(g, c).Run(nil)
	if out != Joined {
		t.Fatalf("got %v", out)
	}
	for _, at := range append(g.clicks, g.enters...) {
		if !at.Before(away0) && at.Before(away1) {
			t.Errorf("input sent at %v while DayZ wasn't in front", at.Sub(start))
		}
	}
}

func TestOtherPopupWaits(t *testing.T) {
	c := &fakeClock{start}
	g := newSim(c)
	g.popup = detect.PopupOther
	g.joinOn = 2
	out, _ := newRunner(g, c).Run(nil)
	if out != Joined || len(g.enters) != 1 {
		t.Fatalf("got %v with %d enters", out, len(g.enters))
	}
	if d := g.enters[0].Sub(g.clicks[0]) - 700*time.Millisecond; d < otherPopupWait {
		t.Errorf("pressed Enter %v after an unknown popup", d)
	}
}

func TestStuckPopup(t *testing.T) {
	c := &fakeClock{start}
	g := newSim(c)
	g.stuckPopup = true
	out, err := newRunner(g, c).Run(nil)
	if out != Failed || !errors.Is(err, ErrPopupStuck) || len(g.enters) != maxEnters {
		t.Fatalf("got %v, %v after %d enters", out, err, len(g.enters))
	}
}

func TestMouseMoved(t *testing.T) {
	c := &fakeClock{start}
	g := newSim(c)
	g.moved = true
	out, _ := newRunner(g, c).Run(nil)
	if out != MouseMoved || len(g.clicks) != 0 {
		t.Fatalf("got %v after %d clicks", out, len(g.clicks))
	}
}

func TestShortFlickerIsNotJoined(t *testing.T) {
	c := &fakeClock{start}
	g := newSim(c)
	g.joinOn = 4
	g.state = detect.Away
	g.after(time.Second, detect.Browser)
	out, _ := newRunner(g, c).Run(nil)
	if out != Joined || len(g.clicks) != 4 {
		t.Fatalf("got %v after %d clicks", out, len(g.clicks))
	}
}

func TestNoAnswerTriesAgain(t *testing.T) {
	c := &fakeClock{start}
	g := newSim(c)
	g.popup = detect.Browser // the click does nothing
	r := newRunner(g, c)
	r.GiveUp = 30 * time.Second
	r.Run(nil)
	if len(g.clicks) != 3 {
		t.Errorf("%d tries in 30s with no answer, want 3", len(g.clicks))
	}
}

func TestSlotOpenedTriesSooner(t *testing.T) {
	c := &fakeClock{start}
	g := newSim(c)
	g.joinOn = 2
	slot := make(chan struct{}, 1)
	r := newRunner(g, c)
	r.MinGap, r.MaxGap = 20*time.Second, 20*time.Second
	r.SlotOpened = slot
	// Signal once the first popup is closed.
	g2 := &nudger{sim: g, slot: slot}
	r.Game = g2
	r.Run(nil)
	if len(g.clicks) != 2 {
		t.Fatalf("%d clicks", len(g.clicks))
	}
	if gap := g.clicks[1].Sub(g.enters[0]); gap > time.Second {
		t.Errorf("next try %v after the popup, want under a second", gap)
	}
}

type nudger struct {
	*sim
	slot chan struct{}
}

func (n *nudger) PressEnter() error {
	n.slot <- struct{}{}
	return n.sim.PressEnter()
}

// The game moving the mouse on the queue screen still counts as joined.
func TestMouseMovedAfterJoining(t *testing.T) {
	c := &fakeClock{start}
	g := &movesOnJoin{sim: newSim(c)}
	g.joinOn = 2
	out, _ := newRunner(g, c).Run(nil)
	if out != Joined {
		t.Fatalf("got %v", out)
	}
}

type movesOnJoin struct{ *sim }

func (m *movesOnJoin) MouseMoved() bool { return m.state == detect.Away }
