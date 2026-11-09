package items

import (
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"time"

	"mkw-server/common"
)

// Limits are the game's item availability rules (source: Wexos's MKW item tool,
// https://wexos.github.io/Tools/mkwitem.html). An item that isn't available is left out of
// the roll and the remaining chances are rescaled, like the game does.
type Limits struct {
	// VS only: an item can't be pulled until the race clock reaches this.
	FirstAvailable map[common.ItemType]time.Duration
	// Room-wide: after an item is pulled, nobody can pull it again for this long.
	Cooldown map[common.ItemType]time.Duration
	// Room-wide: an item can't be pulled while this many of its object are in play.
	MaxInPlay map[common.ItemType]int
}

var DefaultLimits = Limits{
	FirstAvailable: map[common.ItemType]time.Duration{
		common.BlueShell: 30 * time.Second,
		common.Lightning: 30 * time.Second,
		common.Blooper:   15 * time.Second,
		common.POWBlock:  20 * time.Second,
	},
	Cooldown: map[common.ItemType]time.Duration{
		common.Lightning: 30 * time.Second,
		common.Blooper:   15 * time.Second,
		common.POWBlock:  20 * time.Second,
	},
	// Thunder Cloud has no timer: it's available again as soon as the last one is gone.
	MaxInPlay: map[common.ItemType]int{
		common.GreenShell:     12,
		common.RedShell:       8,
		common.Banana:         16,
		common.FakeItemBox:    6,
		common.Mushroom:       12,
		common.BobOmb:         3,
		common.BlueShell:      1,
		common.Lightning:      1,
		common.Star:           3,
		common.GoldenMushroom: 2,
		common.MegaMushroom:   2,
		common.Blooper:        1,
		common.POWBlock:       1,
		common.ThunderCloud:   1,
		common.BulletBill:     1,
	},
}

// Engine decides items for a room. Randomness comes from the server, so a client can't
// predict or pick its roll.
type Engine struct {
	slot        *ItemSlot
	rng         *rand.Rand
	limits      Limits
	lastGranted map[common.ItemType]time.Time
}

// NewEngine makes an engine. limits nil means DefaultLimits.
func NewEngine(slot *ItemSlot, limits *Limits) (*Engine, error) {
	for _, idx := range []int{TableOnlineVS, TableOnlineBalloonBattle, TableOnlineCoinRunners} {
		if idx >= len(slot.Tables) {
			return nil, fmt.Errorf("ItemSlot has %d tables, need table %d", len(slot.Tables), idx)
		}
	}

	var seed [32]byte
	if _, err := crand.Read(seed[:]); err != nil {
		return nil, err
	}

	if limits == nil {
		limits = &DefaultLimits
	}

	return &Engine{
		slot:        slot,
		rng:         rand.New(rand.NewChaCha8(seed)),
		limits:      *limits,
		lastGranted: make(map[common.ItemType]time.Time),
	}, nil
}

// Request describes the player asking for an item.
type Request struct {
	Mode common.GameMode
	// VS: the player's rank (1-based) and how many players are racing.
	Rank        int
	PlayerCount int
	// Battle: how many points/coins the player is behind the leader.
	BehindLeader int
	// VS: time since the race clock started, for Limits.FirstAvailable.
	RaceTime time.Duration
	// Room-wide count of each item object in play (held, trailing, or granted and not yet
	// shown), for Limits.MaxInPlay.
	InPlay map[common.ItemObject]int
	// The player is in a Star, Mega Mushroom or Bullet Bill: no Thunder Cloud then.
	Invincible bool
	// The player is in a Mega Mushroom: no second Mega then.
	InMega bool
}

func tableFor(mode common.GameMode) (int, error) {
	switch mode {
	case common.ModeVS:
		return TableOnlineVS, nil
	case common.ModeBalloonBattle:
		return TableOnlineBalloonBattle, nil
	case common.ModeCoinRunners:
		return TableOnlineCoinRunners, nil
	default:
		return 0, fmt.Errorf("no item table for mode %v", mode)
	}
}

// vsRows lists, for each room size, which of the 12 position columns its positions use:
// smaller rooms use a subset of the 12 player odds. From Wexos's item tool
// (https://wexos.github.io/Tools/mkwitem.html); checked against the game's table.
var vsRows = [...][]int{
	2:  {0, 3},
	3:  {0, 2, 5},
	4:  {0, 2, 5, 8},
	5:  {0, 2, 4, 6, 9},
	6:  {0, 2, 4, 6, 8, 10},
	7:  {0, 2, 4, 6, 8, 9, 11},
	8:  {0, 1, 3, 5, 6, 8, 9, 11},
	9:  {0, 1, 3, 4, 6, 7, 8, 10, 11},
	10: {0, 1, 2, 4, 5, 6, 7, 9, 10, 11},
	11: {0, 1, 2, 3, 4, 6, 7, 8, 9, 10, 11},
	12: {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11},
}

// Column picks the table column for a request.
//
// VS tables have 12 columns, one per position in a 12 player room; smaller rooms use the
// subset in vsRows.
//
// Battle tables have 3 columns based on distance behind the leader (Balloon Battle:
// <3, 3-5, >5 points; Coin Runners: <4, 4-7, >7 coins).
func Column(req Request, columns int) int {
	clamp := func(c int) int { return max(0, min(c, columns-1)) }

	switch req.Mode {
	case common.ModeVS:
		players := max(2, min(req.PlayerCount, len(vsRows)-1))
		rows := vsRows[players]
		rank := max(1, min(req.Rank, players))
		return clamp(rows[rank-1])
	case common.ModeBalloonBattle:
		switch {
		case req.BehindLeader < 3:
			return clamp(0)
		case req.BehindLeader <= 5:
			return clamp(1)
		default:
			return clamp(2)
		}
	case common.ModeCoinRunners:
		switch {
		case req.BehindLeader < 4:
			return clamp(0)
		case req.BehindLeader <= 7:
			return clamp(1)
		default:
			return clamp(2)
		}
	}
	return 0
}

// Decide rolls an item for a request. Unavailable items are left out of the roll; if
// nothing in the column is available the player gets a Mushroom.
func (e *Engine) Decide(req Request, now time.Time) (common.ItemType, error) {
	tableIdx, err := tableFor(req.Mode)
	if err != nil {
		return common.NoItem, err
	}
	table := &e.slot.Tables[tableIdx]
	chances := table.Chances[Column(req, table.Columns())]

	total, available := 0, 0
	var weights [common.ItemSlotRowCount]int
	for i, c := range chances {
		total += c
		if c > 0 && e.Available(common.ItemType(i), req, now) {
			weights[i] = c
			available += c
		}
	}
	if total == 0 {
		return common.NoItem, fmt.Errorf("table %d column has no items", tableIdx)
	}

	item := common.Mushroom
	if available > 0 {
		roll := e.rng.IntN(available)
		for i, c := range weights {
			if roll < c {
				item = common.ItemType(i)
				break
			}
			roll -= c
		}
	}

	if _, limited := e.limits.Cooldown[item]; limited {
		e.lastGranted[item] = now
	}
	return item, nil
}

// Available reports whether item can be pulled right now.
func (e *Engine) Available(item common.ItemType, req Request, now time.Time) bool {
	if item == common.ThunderCloud && req.Invincible {
		return false
	}
	if item == common.MegaMushroom && req.InMega {
		return false
	}
	if first, ok := e.limits.FirstAvailable[item]; ok && req.Mode == common.ModeVS && req.RaceTime < first {
		return false
	}
	if cooldown, ok := e.limits.Cooldown[item]; ok {
		if last, granted := e.lastGranted[item]; granted && now.Sub(last) < cooldown {
			return false
		}
	}
	if limit, ok := e.limits.MaxInPlay[item]; ok && req.InPlay[item.Object()] >= limit {
		return false
	}
	return true
}

// SeedForTest replaces the RNG with a deterministic one.
func (e *Engine) SeedForTest(seed uint64) {
	var s [32]byte
	binary.LittleEndian.PutUint64(s[:], seed)
	e.rng = rand.New(rand.NewChaCha8(s))
}
