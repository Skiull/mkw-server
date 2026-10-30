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

// Shrink (Lightning) durations by row, from the community: row = position - 1 +
// (12 - players), so with fewer players everyone gets a shorter shrink (10 players: 1st
// gets 12-player 3rd place's). Plus 72 frames (1.2 s) for the tumble, all spent small.
var shockDurations = [12]float64{11.5, 10.8, 10.0, 9.3, 8.5, 7.5, 6.5, 5.5, 4.5, 3.5, 2.5, 1.5}

const (
	shockTumble = 1200 * time.Millisecond
	// Margins: speed takes a moment to drop after the hit, and the hit time on the server
	// is off by the packet's lag.
	shockSettle = time.Second
	shockMargin = time.Second
	// Speed while shrunk: x0.7 (Kinoko calcShock: drops 0.03/frame to 0.7).
	shockSpeedFactor = 0.7
)

// shockDuration is how long a player at position (1-12) stays small in a room of players.
func shockDuration(position, players int) time.Duration {
	row := position - 1 + 12 - players
	row = max(0, min(11, row))
	return time.Duration(shockDurations[row]*float64(time.Second)) + shockTumble
}

// startShock notes a Lightning hit on p: they're small for a while. A Star or Bullet Bill
// dodges it, and a Mega is cancelled instead. Caller must hold mu.
func startShock(p *Player, now time.Time) {
	if p.invincible(now) || p.rank == 0 {
		return
	}
	p.shockFrom = now
	p.shockUntil = now.Add(shockDuration(int(p.rank), racingPlayerCount()))
}

// shrunk reports whether p is certainly small now (inside the margins). Caller must hold mu.
func (p *Player) shrunk(now time.Time) bool {
	return !p.shockFrom.IsZero() && now.After(p.shockFrom.Add(shockSettle)) && now.Before(p.shockUntil.Add(-shockMargin))
}
