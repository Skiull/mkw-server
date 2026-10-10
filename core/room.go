package core

import (
	"encoding/binary"
	"fmt"
	"net"
	"time"

	"mkw-server/logging"
	"mkw-server/util"
)

// WFCTalkerInterface allows Room/Aid to interact with WFC without circular dependency
type WFCTalkerInterface interface {
	SendPacketDataToWFC(data []byte) error
}

type Room struct {

	// UDP connection for the room, all aids send/receive from this (hopefully this won't a large bottleneck with 12 aids)
	conn      net.PacketConn
	addr      *net.UDPAddr
	broadcast chan Packet // channel for broadcasting packets to all aids

	aidBitmap uint32

	// Indicates if the countdown can start. Set when all aids are ready and reset when
	// all aids started the countdown.
	countdownState bool

	// Flag for if everyone in the room has finished the current race.
	everyoneFinishedRace bool
}

var room *Room

func InitRoom(roomAddr *net.UDPAddr) error {
	room = &Room{
		addr: roomAddr,
		// 256 came out of nowhere, needs to be tested
		broadcast: make(chan Packet, 256),
	}

	logging.Log("Room created successfully!", roomAddr.String())
	return nil
}

// Starts the listener and broadcaster goroutines
func StartRoom() {
	if room.addr == nil {
		logging.Log("Room address is nil, cannot start room")
		return
	}

	if room.conn != nil {
		logging.Log("Room is already started")
		return
	}

	conn, err := net.ListenPacket("udp", room.addr.String())
	if err != nil {
		logging.Log("Failed to start room at address %s: %v", room.addr.String(), err)
		return
	}
	logging.Log("Room listening on %s", room.addr.String())

	room.conn = conn

	go readLoop()
	go broadcastLoop()
	go pingLoop()
}

func readLoop() {
	buf := make([]byte, 512)
	for {
		n, addr, err := room.conn.ReadFrom(buf)
		if err != nil {
			logging.Log("Error reading from connection: %v", err)
			return
		}

		a := aids[addr.String()]
		if a == nil {
			logging.Log("Non-aid sent a packet. Address: %v", addr)
			continue
		}

		pkt := Packet{
			sender:       addr,
			aid:          a,
			data:         append([]byte{}, buf[:n]...),
			receivedTime: time.Now(),
		}

		handlePacket(&pkt)
	}
}

func handlePacket(pkt *Packet) {
	data := pkt.data
	a := pkt.aid

	if !a.readyForCountdown && isReadyPacket(data) {
		handleAidReady(a)
		return
	}

	if a.readyForCountdown && hasCountdownStarted(data) {
		handleAidStartedCountdown(a)
		return
	}

	if isPongPacket(data) {
		handlePongPacket(a)
		return
	}

	if len(data) == a.raceFrameSize && isRaceFramePacket(binary.BigEndian.Uint16(data)) {
		err := handleRaceFrame(a, data)
		if err != nil {
			logging.Log("aid %d handleRaceFrame err %v", a.aid, err)
		}
		return
	}

	if isFinishTimePacket(data) {
		err := handleFinishTimePacket(a, data)
		if err != nil {
			logging.Log("aid %d finish time handling error: %v", a.aid, err)
		}
		return
	}

	if isRacePacket(data) {
		err := handleRacePacket(a, data)
		if err != nil {
			logging.Log("aid %d handleRacePacket() failed: %v", a.aid, err)
			return
		}
		room.broadcast <- *pkt
		return
	}
}

func broadcastLoop() {
	for pkt := range room.broadcast {
		sender := aids[pkt.sender.String()]

		// sender can be nil if they disconnect between sending the packet and the broadcast
		if sender == nil {
			continue
		}

		data := pkt.data
		sendersAid := data[1]

		if sendersAid != sender.aid {
			continue
		}

		if !sender.isRacer && containsSelect(data) {
			logging.Log("Aid %d sent select!", sender.aid)
			sender.isRacer = true
		}

		aidBitmap := binary.BigEndian.Uint16(data[2:4])
		receivingAids := util.GetSendToAids(aidBitmap)

		for _, aid := range *receivingAids {
			a := getAid(aid)
			if a == nil {
				// This can happen when someone left, but wfc-server hasn't yet informed other aids yet
				continue
			}

			if aid == sendersAid {
				logging.Log("Attempted to send from %d to %d", sendersAid, aid)
				continue
			}

			_, err := room.conn.WriteTo(pkt.data, a.addr)
			if err != nil {
				logging.Log("Error writitng to aid %d", a.aid)
				continue
			}
		}
	}
}

// Sends a ping to all aids every 1/2 seconds.
func pingLoop() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		for _, a := range aids {
			if a == nil {
				continue
			}
			sendPing(a)
		}
	}
}

func getAid(aid byte) *Aid {
	for addr, a := range aids {
		if addr == "" || a == nil {
			continue
		}

		if a.aid == aid {
			return a
		}
	}
	return nil
}

func AddAidToRoom(aidAddr string, aid byte, hasGuest bool) error {
	if _, exists := aids[aidAddr]; exists {
		return fmt.Errorf("Aid %s already exists in room", aidAddr)
	}

	room.aidBitmap = util.SetAid(room.aidBitmap, aid)
	a, err := NewAid(aidAddr, room, aid, hasGuest)
	if err != nil {
		return fmt.Errorf("Failed to create aid %s due to", aidAddr)
	}

	aids[aidAddr] = a

	logging.Log("Successfully added aid %s (aid: %d). Aid count is %d", aidAddr, a.aid, GetCurrentAidCount())
	return nil
}

func RemoveAidFromRoom(aidAddr string) error {
	a, _ := aids[aidAddr]

	if a == nil {
		return fmt.Errorf("Aid %s not in room, can't remove", aidAddr)
	}

	room.aidBitmap = util.ClearAid(room.aidBitmap, a.aid)

	logging.Log("Successfully removed aid %s (aid: %d). Aid count is %d", aidAddr, a.aid, GetCurrentAidCount())

	delete(aids, aidAddr)
	return nil
}

// gets the aid if the aid is the room's host
func GetHost(aidAddr string) *Aid {
	a, _ := aids[aidAddr]

	if a == nil {
		logging.Log("Aid %s not in room, can't remove")
		return nil
	}

	return nil
}

func GetRoomAddr() string {
	return room.addr.String()
}

func GetCurrentAidCount() int {
	return len(aids)
}

func CloseRoom() {
	room.conn.Close()
}

func RoomInitialized() bool {
	return room != nil
}

func isAidInRoom(aid byte) bool {
	for _, a := range aids {
		if a == nil {
			continue
		}

		if a.aid == aid {
			return true
		}
	}
	return false
}
