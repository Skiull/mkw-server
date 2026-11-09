package core

import (
	"mkw-server/items"
)

// Offroad check: offroad slows a vehicle to a fraction of its speed, set per vehicle and
// character in kartParam.bin/driverParam.bin (about 55-91% on weak offroad, 33-71% on
// offroad, 19-42% on heavy offroad), unless a boost carries it:
// Mushrooms (from the slot or the ground), Golden Mushrooms, Stars, Megas, Bullet Bills,
// mini-turbos, tricks, boost panels and cannons. The game doesn't slow a vehicle down
// instantly, so the limit only applies once the player has been on offroad, with no boost,
// for offroadSettleFrames, and only to speed averaged after that point.

const (
	// Time for a vehicle to slow to offroad speed after entering offroad or a boost ending.
	offroadSettleFrames = 40
	// Frames the offroad speed is averaged over (shorter than the general check's window).
	offroadWindowFrames = 20
	// How long a mini-turbo, trick or Mushroom boost may lift the speed after it starts
	// (or, for a Mushroom, after the boost flag clears).
	boostFrames = 90
	// Star and Mega: they ignore offroad while they last.
	powerItemFrames = 10 * 60
	// Slack over the measured fraction of the 120 u/f cap.
	offroadSlack      = 1.15
	offroadExtraSpeed = 5
)

func isOffroad(ty uint8) bool {
	return ty == items.FloorWeakOffroad || ty == items.FloorOffroad || ty == items.FloorHeavyOffroad
}

// offroadFactor is how much speed the player's combo keeps on an offroad type. Without
// stats, the most lenient vehicle and character are assumed.
func offroadFactor(m *moveState, ty uint8) float32 {
	if m.stats == nil {
		return map[uint8]float32{items.FloorWeakOffroad: 0.91, items.FloorOffroad: 0.72, items.FloorHeavyOffroad: 0.43}[ty]
	}
	return m.stats.FloorSpeed(m.vehicle, m.character, ty)
}

// trackOffroad updates the current offroad stretch from the floor under pos.
// boosted covers boosts the race packet doesn't show (Mega, leaf-pile Mushroom).
func trackOffroad(m *moveState, frame uint32, pos [3]float32, boosted bool) {
	ty, onFloor := m.floors.At(pos)
	if onFloor && (ty == items.FloorBoostPanel || ty == items.FloorBoostRamp) {
		m.boostUntil = max(m.boostUntil, frame+boostFrames)
	}
	offroad := isOffroad(ty)
	boosting := frame < m.boostUntil || boosted || (frame < m.bulletUntil && !m.bulletEnded)
	if !onFloor || !offroad || boosting {
		m.offroadSince = 0
		return
	}
	switch {
	case m.offroadSince == 0:
		m.offroadSince, m.offroadType = frame, ty
	case offroadFactor(m, ty) > offroadFactor(m, m.offroadType):
		m.offroadType = ty // Lighter offroad in the same stretch: be lenient.
	}
}

// offroadLimit returns the speed limit for a window starting at windowStart, if the player
// has been settled on offroad for the whole window.
func offroadLimit(m *moveState, windowStart uint32) (float32, bool) {
	if m.offroadSince == 0 || windowStart < m.offroadSince+offroadSettleFrames {
		return 0, false
	}
	// From the combo's own top speed when its stats are known (as the combo speed check):
	// based on the 120 cap, weak offroad (sand, shallow water) allowed more speed than any
	// vehicle has, so a no-offroad code went unnoticed there.
	top := float32(120)
	if m.stats != nil {
		if base := m.stats.BaseSpeed(m.vehicle, m.character); base > 0 {
			mul := float32(1)
			if items.IsBike(m.vehicle) {
				mul += wheelieBonus
			}
			top = base * engineSpeedFactor * mul
		}
	}
	return top*offroadFactor(m, m.offroadType)*offroadSlack + offroadExtraSpeed, true
}

func floorName(ty uint8) string {
	switch ty {
	case items.FloorWeakOffroad:
		return "weak offroad"
	case items.FloorOffroad:
		return "offroad"
	case items.FloorHeavyOffroad:
		return "heavy offroad"
	}
	return "floor"
}
