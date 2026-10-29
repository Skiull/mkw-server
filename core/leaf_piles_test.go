package core

import (
	"testing"
	"time"

	"mkw-server/common"
	"mkw-server/items"
)

func TestLeafPileItem(t *testing.T) {
	now := time.Now()
	room.config.Courses = map[common.Course]*items.Course{
		common.MapleTreeway: {LeafPiles: []items.LeafPile{{Item: 1, Chance: 65}, {Item: 4, Chance: 20}}},
	}
	defer func() { room.config.Courses = nil }()
	a := &Aid{players: []*Player{newPlayer(0, false)}, course: common.MapleTreeway}
	p := a.players[0]
	if leafPileItem(a, common.ObjMushroom, now) {
		t.Fatal("Mushroom allowed without touching a pile")
	}
	p.leafPileAt, p.leafPile = now.Add(-3*time.Second), 0
	if !leafPileItem(a, common.ObjMushroom, now) || !leafPileItem(a, common.ObjBanana, now) {
		t.Fatal("pile item refused after touching a pile")
	}
	if leafPileItem(a, common.ObjStar, now) {
		t.Fatal("Star allowed from a Mushroom/Banana pile")
	}
	if leafPileItem(a, common.ObjBlueShell, now) {
		t.Fatal("Blue Shell allowed from a leaf pile")
	}
	if leafPileItem(a, common.ObjMushroom, now.Add(time.Minute)) {
		t.Fatal("pile item allowed long after the pile")
	}

	// Setting 4 piles give only Stars.
	p.leafPile = 1
	if leafPileItem(a, common.ObjMushroom, now) || !leafPileItem(a, common.ObjStar, now) {
		t.Fatal("confirmed pile setting not applied")
	}
}

func TestLightningEndsMega(t *testing.T) {
	user := &Aid{players: []*Player{newPlayer(0, false)}}
	hit := &Aid{players: []*Player{newPlayer(0, false)}}
	saved := aids
	aids = map[string]*Aid{"a": user, "b": hit}
	defer func() { aids = saved }()
	now := time.Now()
	user.players[0].megaAt, hit.players[0].megaAt = now, now
	lightningStruck(user)
	if !hit.players[0].megaAt.IsZero() {
		t.Fatal("shocked player kept the Mega speed limit")
	}
	if user.players[0].megaAt.IsZero() {
		t.Fatal("Lightning user lost their Mega")
	}
}
