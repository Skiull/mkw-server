package core

import (
	"fmt"
	"time"

	"mkw-server/core/records"
	"mkw-server/items"
	"mkw-server/logging"
)

// No-hit checks (log only). A player's own game decides whether an item hit them, which is
// what "no hit" codes abuse. The server can't see shells, but some hits are certain:
//   - ramming: driving into someone while in a Star, Mega or Bullet Bill;
//   - Lightning: hits everyone else who isn't invincible;
//   - Blue Shell: hits someone (normally the leader) within a few seconds;
//   - driving through a tracked ground item (ground_items.go).
// A no-hit code also stops the game reporting item hits (EVENT HIT-FREE), and those reports
// also come from the player's own items hitting things, so they aren't used.
// Each expected hit must show up from the victim as a hit flag flipping (RACEDATA 0x2C) or
// a clear slowdown. If neither does, it's logged as a possible no-hit code.

const (
	ramRadius     = 350 // Center to center, in course units.
	ramDeadline   = 1500 * time.Millisecond
	lightningWait = 3 * time.Second
	blueShellWait = 25 * time.Second
	hitSlowdown   = 0.6 // Speed at or below this fraction of the speed at the hit counts.
	hitStopSpeed  = 30  // Or at or below this, in u/f.
	// Below this speed, items without a hit flag can't be checked (no slowdown to see).
	hitMinSpeed     = 45
	ramPositionAge  = 250 * time.Millisecond
	ramRepeatPeriod = 3 * time.Second
	// After the game reports a hit, speed must drop within this long.
	hitSlowWait  = 1500 * time.Millisecond
	hitKeptSpeed = 0.8
	// KartActions that don't always slow the kart: 8 Cataquack launch.
	hitActionsNoSlow = 1 << 8
)

type hitExpectation struct {
	victim   *Player
	aid      *Aid
	reason   string
	at       time.Time
	deadline time.Time
	speedAt  float32
	minSpeed float32
	anyone   bool // Blue Shell: a hit on anyone in the room counts.
	// Log only, never kick: whether it hit depends on what each game saw (lag).
	logOnly bool
	// The tracked ground item driven through, if that's the reason.
	item *groundItem
	// The player's own game said it got hit: only a slowdown counts (a "keep speed when
	// hit" code still spins but doesn't slow down).
	needSlow bool
}

// Guarded by mu.
var (
	hitExpectations []*hitExpectation
	lastBlueHitAt   time.Time // Last Blue Shell hit flag flip from anyone.
)

// observeHits notes a player's hit flags and resolves expectations. Call after
// checkMovement for each RaceData record. Caller must hold mu.
func observeHits(a *Aid, p *Player, rd *records.RaceData, now time.Time) {
	flags, blue := rd.HitFlags(), rd.BlueShellHitFlags()
	if p.hitFlagsSeen {
		if flags != p.hitFlags {
			p.hitAt = now
		}
		if blue != p.blueHitFlags {
			lastBlueHitAt = now
		}
	}
	if zap := rd.CloudZapFlip(); p.hitFlagsSeen && zap != p.cloudZap {
		cloudUsedAt = time.Time{} // The Thunder Cloud is gone.
	}
	p.cloudZap = rd.CloudZapFlip()
	actions := rd.KartActions() &^ hitActionsNoSlow
	if p.hitFlagsSeen && actions != p.kartActions && !p.invincible(now) && p.move.lastSpeed >= hitMinSpeed {
		e := expectHit(a, p, fmt.Sprintf("hit (KartAction bits %#x) without losing speed", actions^p.kartActions), now, hitSlowWait, false)
		e.needSlow = true
	}
	p.kartActions = actions
	p.hitFlags, p.blueHitFlags, p.hitFlagsSeen = flags, blue, true
	if rd.Respawning() {
		p.respawnedAt = now
	}

	if rd.HasPosition() {
		pos := rd.WorldPos()
		if !p.lastPosAt.IsZero() && now.Sub(p.lastPosAt) < time.Second && !rd.Respawning() {
			checkGroundItems(a, p, p.lastPos, pos, now)
		}
		p.lastPos, p.lastPosAt = pos, now
		checkRams(a, p, now)
	}
	resolveHits(now)
}

// checkRams expects a hit on p if someone in a Star, Mega or Bullet Bill is on top of them.
func checkRams(a *Aid, p *Player, now time.Time) {
	if p.invincible(now) {
		return // Two invincible players just bounce.
	}
	for _, other := range aids {
		if other == a || other.kicked || other.bot != nil {
			continue
		}
		for _, q := range other.players {
			if !q.invincible(now) || now.Sub(q.lastPosAt) > ramPositionAge {
				continue
			}
			if items.Distance(p.lastPos, q.lastPos) > ramRadius || now.Sub(p.rammedAt) < ramRepeatPeriod {
				continue
			}
			p.rammedAt = now
			// Each game sees the others a little late, so a ram on one screen can be a miss on
			// another (a Bullet Bill moves ~145 u/f). Logged, never kicked.
			e := expectHit(a, p, fmt.Sprintf("rammed by aid %d (%s)", other.aid, q.powerName(now)), now, ramDeadline, false)
			e.logOnly = true
		}
	}
}

func (p *Player) powerName(now time.Time) string {
	switch {
	case within(p.starAt, starDuration, now):
		return "Star"
	case within(p.megaAt, megaItemWindow, now):
		return "Mega"
	default:
		return "Bullet Bill"
	}
}

// expectLightning expects a hit on every player but the user who isn't invincible.
func expectLightning(user *Aid, now time.Time) {
	for _, a := range aids {
		if a == user || a.kicked || a.bot != nil || !a.raceStarted {
			continue
		}
		for _, p := range a.players {
			if !p.invincible(now) {
				expectHit(a, p, fmt.Sprintf("Lightning from aid %d", user.aid), now, lightningWait, false)
			}
		}
	}
}

// expectBlueShell expects a Blue Shell hit on someone in the room; the current leader is
// named in the log.
func expectBlueShell(user *Aid, now time.Time) {
	for _, a := range aids {
		if a == user || a.kicked || a.bot != nil || !a.raceStarted {
			continue
		}
		for _, p := range a.players {
			if p.rank == 1 {
				// The leader can change while it flies: logged, never kicked.
				e := expectHit(a, p, fmt.Sprintf("Blue Shell from aid %d", user.aid), now, blueShellWait, true)
				e.logOnly = true
				return
			}
		}
	}
}

func expectHit(a *Aid, p *Player, reason string, now time.Time, wait time.Duration, anyone bool) *hitExpectation {
	e := &hitExpectation{
		victim: p, aid: a, reason: reason, at: now, deadline: now.Add(wait),
		speedAt: p.move.lastSpeed, minSpeed: p.move.lastSpeed, anyone: anyone,
	}
	hitExpectations = append(hitExpectations, e)
	return e
}

// resolveHits drops expectations that came true and logs (and returns) the ones that ran
// out.
func resolveHits(now time.Time) (missed []*hitExpectation) {
	kept := hitExpectations[:0]
	for _, e := range hitExpectations {
		p := e.victim
		e.minSpeed = min(e.minSpeed, p.move.lastSpeed)
		flagged := p.hitAt.After(e.at) || (e.anyone && lastBlueHitAt.After(e.at))
		slowed := e.minSpeed <= hitStopSpeed || (e.speedAt > hitStopSpeed && e.minSpeed <= e.speedAt*hitSlowdown)
		hit := flagged || slowed
		if e.needSlow {
			// A real hit drops speed to a fraction (recorded: 140->12, 70->14); a "keep speed"
			// code holds 85-95%. Lenient so a soft hit isn't kicked.
			hit = e.minSpeed <= hitStopSpeed || e.minSpeed <= e.speedAt*hitKeptSpeed
		}
		switch {
		case hit, e.aid.kicked:
		case p.launchedAt.After(e.at.Add(-time.Second)) || p.respawnedAt.After(e.at):
			// A cannon or a respawn moves the kart on its own: its speed says nothing. A hit
			// reported as the kart enters a cannon can come after the launch started.
		case e.speedAt < hitMinSpeed:
			// Already slow: a hit can't be told apart from driving slowly. No verdict. (The
			// Fake Item Box hit flag doesn't flip on real hits, so it can't be relied on.)
		case p.invincible(now) && !e.anyone && !e.needSlow:
			// Became invincible in time (a Star or Bullet Bill dodges it).
		case now.After(e.deadline):
			logging.Log("Aid %d player %d possible no-hit code: %s; speed only fell %.0f -> %.0f u/f",
				e.aid.aid, playerIndex(e.aid, p), e.reason, e.speedAt, e.minSpeed)
			missed = append(missed, e)
			recordFlag(e.aid, "possible no-hit: "+e.reason, now)
			if room.config.KickNoHit && !e.logOnly && !e.aid.kicked {
				logging.Log("Aid %d no-hit cheat: %s", e.aid.aid, e.reason)
				kick(e.aid, KickCheating)
			}
		default:
			kept = append(kept, e)
		}
	}
	hitExpectations = kept
	return missed
}

func playerIndex(a *Aid, p *Player) int {
	for i, q := range a.players {
		if q == p {
			return i
		}
	}
	return -1
}

// resetHitChecks drops pending expectations (new race). Caller must hold mu.
func resetHitChecks() {
	hitExpectations = nil
	resetGroundItems()
}
