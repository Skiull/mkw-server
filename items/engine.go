package items

import (
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"time"

	"mkw-server/common"
)

// DefaultCooldowns are room-wide cooldowns after a strong item is handed out. While an
// item is on cooldown, a roll that lands on it gives a Mushroom instead, like the game
// does for its own item limits. Operators can tune these.
var DefaultCooldowns = map[common.ItemType]time.Duration{
	common.BlueShell:    30 * time.Second,
	common.Lightning:    30 * time.Second,
	common.POWBlock:     30 * time.Second,
	common.Blooper:      15 * time.Second,
	common.ThunderCloud: 30 * time.Second,
}

// Engine decides items for a room. Randomness comes from the server, so a client can't
// predict or pick its roll.
type Engine struct {
	slot        *ItemSlot
	rng         *rand.Rand
	cooldowns   map[common.ItemType]time.Duration
	lastGranted map[common.ItemType]time.Time
}

func NewEngine(slot *ItemSlot, cooldowns map[common.ItemType]time.Duration) (*Engine, error) {
	for _, idx := range []int{TableOnlineVS, TableOnlineBalloonBattle, TableOnlineCoinRunners} {
		if idx >= len(slot.Tables) {
			return nil, fmt.Errorf("ItemSlot has %d tables, need table %d", len(slot.Tables), idx)
		}
	}

	var seed [32]byte
	if _, err := crand.Read(seed[:]); err != nil {
		return nil, err
	}

	if cooldowns == nil {
		cooldowns = DefaultCooldowns
	}

	return &Engine{
		slot:        slot,
		rng:         rand.New(rand.NewChaCha8(seed)),
		cooldowns:   cooldowns,
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

// Column picks the table column for a request.
//
// VS tables have 12 columns, one per position in a 12 player room. Smaller rooms are
// spread over all 12 so 1st always gets 1st place odds and last always gets 12th place
// odds. The game resamples its tables for smaller rooms too; this is an approximation of
// that until its exact formula is confirmed.
//
// Battle tables have 3 columns based on distance behind the leader (Balloon Battle:
// <3, 3-5, >5 points; Coin Runners: <4, 4-7, >7 coins).
func Column(req Request, columns int) int {
	clamp := func(c int) int { return max(0, min(c, columns-1)) }

	switch req.Mode {
	case common.ModeVS:
		if req.PlayerCount <= 1 {
			return 0
		}
		rank := max(1, min(req.Rank, req.PlayerCount))
		// Rounded linear map of [1, PlayerCount] onto [0, columns-1].
		num := (rank - 1) * (columns - 1)
		den := req.PlayerCount - 1
		return clamp((2*num + den) / (2 * den))
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

// Decide rolls an item for a request.
func (e *Engine) Decide(req Request, now time.Time) (common.ItemType, error) {
	tableIdx, err := tableFor(req.Mode)
	if err != nil {
		return common.NoItem, err
	}
	table := &e.slot.Tables[tableIdx]
	chances := table.Chances[Column(req, table.Columns())]

	total := 0
	for _, c := range chances {
		total += c
	}
	if total == 0 {
		return common.NoItem, fmt.Errorf("table %d column has no items", tableIdx)
	}

	roll := e.rng.IntN(total)
	item := common.NoItem
	for i, c := range chances {
		if roll < c {
			item = common.ItemType(i)
			break
		}
		roll -= c
	}

	if e.onCooldown(item, now) {
		item = common.Mushroom
	}

	if _, limited := e.cooldowns[item]; limited {
		e.lastGranted[item] = now
	}

	return item, nil
}

func (e *Engine) onCooldown(item common.ItemType, now time.Time) bool {
	cooldown, limited := e.cooldowns[item]
	if !limited {
		return false
	}
	last, granted := e.lastGranted[item]
	return granted && now.Sub(last) < cooldown
}

// SeedForTest replaces the RNG with a deterministic one.
func (e *Engine) SeedForTest(seed uint64) {
	var s [32]byte
	binary.LittleEndian.PutUint64(s[:], seed)
	e.rng = rand.New(rand.NewChaCha8(s))
}
