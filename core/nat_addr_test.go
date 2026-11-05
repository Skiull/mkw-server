package core

import (
	"net"
	"testing"
)

func TestRoomPacketsFromAnotherPort(t *testing.T) {
	setupRoom(t, Config{})
	a0 := addTestAid(t, 0, false)
	a1 := addTestAid(t, 1, false)

	// Same IP, new port, Race packet from aid 1: matched by the sender byte.
	from := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1024}
	if got := aidForAddr(from, []byte{RacePacketMagic, 1}); got != a1 {
		t.Fatalf("got %v, want aid 1", got)
	}
	if a1.addr.Port != 1024 {
		t.Fatal("replies don't go to the learned port")
	}
	if got := aidForAddr(from, []byte("PO")); got != a1 {
		t.Fatal("learned address not remembered")
	}

	// Two unconfirmed aids on the IP and no sender to tell them apart: no guess.
	a0.stats.received = 0
	if got := aidForAddr(&net.UDPAddr{IP: net.IPv4(127, 0, 0, 2), Port: 5}, []byte("PO")); got != nil {
		t.Fatal("matched an aid on another IP")
	}

	// Removing the aid forgets its learned address.
	if err := RemoveAidFromRoom((&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 40001}).String()); err != nil {
		t.Fatal(err)
	}
	if aidForAddr(from, []byte("PO")) == a1 {
		t.Fatal("removed aid still matched")
	}
}
