package core

import (
	"fmt"
	"strings"
	"time"

	"mkw-server/logging"
)

// Shrunk players (hit by Lightning) have a lower top speed, but how much lower and for how
// long (longer near the front) isn't documented. Until there are real numbers, each
// Lightning hit logs the player's top average speed for every second afterwards; a speed
// limit for shrunk players gets set from those logs.

const shrinkLogSeconds = 12

type shrinkLog struct {
	at       time.Time
	rank     byte
	note     string
	topSpeed [shrinkLogSeconds]float32
}

// startShrinkLog starts logging a Lightning victim's speeds. Caller must hold mu.
func startShrinkLog(a *Aid, p *Player, now time.Time) {
	if a.bot != nil || !a.raceStarted {
		return
	}
	// One Lightning can be reported twice (the event and the emptied item slot).
	if within(p.shrink.at, 2*time.Second, now) {
		return
	}
	var notes []string
	if within(p.starAt, starDuration, now) {
		notes = append(notes, "Star")
	}
	if within(p.bulletAt, bulletDuration, now) && !p.bulletEnded {
		notes = append(notes, "Bullet Bill")
	}
	if within(p.megaAt, megaItemWindow, now) {
		notes = append(notes, "Mega")
	}
	p.shrink = shrinkLog{at: now, rank: p.rank, note: strings.Join(notes, "+")}
}

// trackShrinkSpeed records a Lightning victim's speed and logs it once the window is over.
// Caller must hold mu.
func trackShrinkSpeed(a *Aid, p *Player, now time.Time) {
	s := &p.shrink
	if s.at.IsZero() {
		return
	}
	sec := int(now.Sub(s.at) / time.Second)
	if sec < shrinkLogSeconds {
		s.topSpeed[sec] = max(s.topSpeed[sec], p.move.lastSpeed)
		return
	}
	speeds := make([]string, shrinkLogSeconds)
	for i, v := range s.topSpeed {
		speeds[i] = fmt.Sprintf("%.0f", v)
	}
	note := s.note
	if note == "" {
		note = "none"
	}
	logging.Log("Aid %d hit by Lightning in rank %d (active: %s): top speed each second after: %s u/f",
		a.aid, s.rank, note, strings.Join(speeds, " "))
	p.shrink = shrinkLog{}
}

func within(at time.Time, d time.Duration, now time.Time) bool {
	return !at.IsZero() && now.Sub(at) <= d
}
