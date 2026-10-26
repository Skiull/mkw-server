package core

import (
	"fmt"
	"net"
	"sync"
	"time"

	"mkw-server/items"
	"mkw-server/logging"
	"mkw-server/util"
)

const RacePacketMagic uint8 = 0xb

// Config holds the server-authority settings for a room.
type Config struct {
	// An aid is kicked after KickThreshold invalid packets within KickWindow.
	// 0 disables automatic kicks (invalid packets are still dropped).
	KickThreshold int
	KickWindow    time.Duration

	// Server-side item decisions. Nil leaves items to the clients (vanilla behavior).
	ItemEngine *items.Engine
	// Drop packets and strike aids whose items don't match what the server granted.
	EnforceItems bool
}

func DefaultConfig() Config {
	return Config{
		KickThreshold: 30,
		KickWindow:    10 * time.Second,
	}
}

type Room struct {
	// UDP connection for the room. All aids send to and receive from it.
	conn net.PacketConn
	addr *net.UDPAddr

	aidBitmap uint32

	// Indicates if the countdown can start. Set when all aids are ready and reset when
	// all aids started the countdown.
	countdownState bool

	config Config
}

var room *Room

// mu guards room state and every Aid/Player. Packets are handled one at a time on the read
// goroutine, while wfc-server requests, pings and the countdown run on others.
var mu sync.Mutex

func InitRoom(roomAddr *net.UDPAddr, config Config) error {
	room = &Room{
		addr:   roomAddr,
		config: config,
	}

	logging.Log("Room created successfully! %s", roomAddr.String())
	return nil
}

// Starts the listener and ping goroutines
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
	go pingLoop()
}

func readLoop() {
	buf := make([]byte, 2048)
	for {
		n, addr, err := room.conn.ReadFrom(buf)
		if err != nil {
			logging.Log("Error reading from connection: %v", err)
			return
		}

		data := append([]byte{}, buf[:n]...)

		mu.Lock()
		relayTo := handlePacket(addr, data, time.Now())
		mu.Unlock()

		for _, to := range relayTo {
			if _, err := room.conn.WriteTo(data, to); err != nil {
				logging.Log("Error relaying to %v: %v", to, err)
			}
		}
	}
}

// handlePacket handles one datagram and returns the addresses it should be relayed to.
// Caller must hold mu.
func handlePacket(addr net.Addr, data []byte, now time.Time) []net.Addr {
	a := aids[addr.String()]
	if a == nil {
		logging.Log("Non-aid sent a packet. Address: %v", addr)
		return nil
	}

	if a.kicked || len(data) == 0 {
		return nil
	}

	switch {
	case isReadyPacket(data):
		if !a.readyForCountdown {
			handleAidReady(a)
		}
		return nil
	case isPongPacket(data):
		handlePongPacket(a)
		return nil
	case isItemRequestPacket(data):
		handleItemRequest(a, data, now)
		return nil
	case data[0] != RacePacketMagic:
		return nil
	}

	pkt, err := parseRacePacket(data, a.context())
	if err != nil {
		strike(a, fmt.Sprintf("invalid Race packet: %v", err), now)
		return nil
	}

	if err := checkIdentity(a, pkt); err != nil {
		strike(a, err.Error(), now)
		return nil
	}

	if room.config.ItemEngine != nil {
		if err := observeItems(a, pkt, now); err != nil {
			if room.config.EnforceItems {
				strike(a, fmt.Sprintf("item violation: %v", err), now)
				return nil
			}
			logging.Log("Aid %d item violation (not enforced): %v", a.aid, err)
		}
	}

	if !a.isRacer && pkt.Select != nil {
		logging.Log("Aid %d sent select!", a.aid)
		a.isRacer = true
	}

	if a.readyForCountdown && pkt.IsCountdownStarted() {
		handleAidStartedCountdown(a)
	}

	trackPlayerState(a, pkt)

	var relayTo []net.Addr
	for _, dest := range *util.GetSendToAids(pkt.Destinations) {
		to := getAid(dest)
		// nil when someone left but wfc-server hasn't told the sender yet.
		if to == nil || to.kicked {
			continue
		}
		relayTo = append(relayTo, to.addr)
	}
	return relayTo
}

// checkIdentity pins an aid's friend code to the first one it reports, so a client can't
// impersonate someone else partway through a session. Caller must hold mu.
func checkIdentity(a *Aid, pkt *RacePacket) error {
	if pkt.User == nil {
		return nil
	}

	if a.friendCode == 0 {
		a.friendCode = pkt.User.FriendCode
		return nil
	}

	if pkt.User.FriendCode != a.friendCode {
		return fmt.Errorf("friend code changed from %d to %d", a.friendCode, pkt.User.FriendCode)
	}
	return nil
}

// trackPlayerState records per-player state the server uses for item decisions.
// Caller must hold mu.
func trackPlayerState(a *Aid, pkt *RacePacket) {
	if pkt.RaceInfo != nil {
		started := pkt.IsCountdownStarted()
		if started && !a.raceStarted {
			resetItemState(a)
		}
		a.raceStarted = started
		a.mode = pkt.RaceInfo.GameMode()
	}

	for i, rd := range pkt.RaceData {
		if i < len(a.players) {
			a.players[i].rank = rd.Rank
		}
	}

	if pkt.RaceMode != nil && pkt.RaceMode.Battle != nil {
		for i, score := range pkt.RaceMode.Battle.Scores {
			if i < len(a.players) {
				a.players[i].battleScore = score
			}
		}
	}
}

// Sends a ping to all aids every 1/2 seconds.
func pingLoop() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		mu.Lock()
		for _, a := range aids {
			if a == nil || a.kicked {
				continue
			}
			sendPing(a)
		}
		mu.Unlock()
	}
}

// Caller must hold mu.
func getAid(aid byte) *Aid {
	for _, a := range aids {
		if a != nil && a.aid == aid {
			return a
		}
	}
	return nil
}

func AddAidToRoom(aidAddr string, aid byte, isHost bool, hasGuest bool) error {
	mu.Lock()
	defer mu.Unlock()

	if _, exists := aids[aidAddr]; exists {
		return fmt.Errorf("Aid %s already exists in room", aidAddr)
	}

	if getAid(aid) != nil {
		return fmt.Errorf("Aid number %d is already in use", aid)
	}

	a, err := NewAid(aidAddr, aid, isHost, hasGuest)
	if err != nil {
		return fmt.Errorf("Failed to create aid %s: %w", aidAddr, err)
	}

	room.aidBitmap = util.SetAid(room.aidBitmap, aid)
	aids[aidAddr] = a

	logging.Log("Successfully added aid %s (aid: %d, host: %v, guest: %v). Aid count is %d", aidAddr, a.aid, isHost, hasGuest, len(aids))
	return nil
}

func RemoveAidFromRoom(aidAddr string) error {
	mu.Lock()
	defer mu.Unlock()

	a := aids[aidAddr]
	if a == nil {
		return fmt.Errorf("Aid %s not in room, can't remove", aidAddr)
	}

	room.aidBitmap = util.ClearAid(room.aidBitmap, a.aid)
	delete(aids, aidAddr)

	logging.Log("Successfully removed aid %s (aid: %d). Aid count is %d", aidAddr, a.aid, len(aids))

	// The leaving aid may have been the last one the countdown was waiting on.
	tryUpdateRoomCountdownState()
	return nil
}

func GetRoomAddr() string {
	return room.addr.String()
}

func GetCurrentAidCount() int {
	mu.Lock()
	defer mu.Unlock()
	return len(aids)
}

func CloseRoom() {
	if room.conn != nil {
		room.conn.Close()
	}
}

func RoomInitialized() bool {
	return room != nil
}

// Caller must hold mu.
func isAidInRoom(aid byte) bool {
	return getAid(aid) != nil
}
