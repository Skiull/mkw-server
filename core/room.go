package core

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"mkw-server/common"
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
	// Kick right away (as cheating) anyone holding or using an item the server never granted.
	KickItemCheats bool
	// Kick right away (as cheating) anyone respawning away from the course's respawn point.
	KickRespawnCheats bool

	// Known-good game hashes (see game_hash.go). Nil only logs the hashes clients send.
	GameHashes map[uint64]bool
	// Kick anyone whose game hash isn't in GameHashes, as an unknown game version.
	KickHashCheats bool
	// StaticR.rel .data blocks that change in normal play (data_check.go).
	MutableDataBlocks map[int]bool
	// Kick anyone whose .data changed outside MutableDataBlocks.
	KickDataCheats bool
	// Kick players who don't take a hit the server is sure of (hit_check.go).
	KickNoHit bool
	// Trusted players by profile ID, and where learned values go (trust.go).
	TrustedPlayers map[uint32]bool
	LearnFile      string
	// Folder with our Kinoko build (kinoko.exe, Race/, lines/) for AI bots (ai_bots.go).
	// Empty: bots mirror a real player instead.
	AIBotDir string
	// Rebuild the AI bots' lines from players' runs after each recorded race (human_lines.go).
	LearnLines bool
	// Folder for race recordings (recorder.go). Empty: off.
	RecordDir string
	// Also record raw race-state bytes when they change (diagnostics).
	RecordRaw bool
	// Also record each player's race records as sent, for in-game replays (replay.go).
	RecordPackets bool
	// Play this packet recording back with the room's bots instead of mirroring (replay.go).
	ReplayFile string
	// Replay bots run the recorded race themselves (from when the room opens) for a watcher
	// who joins it in progress and spectates, instead of following the watcher's race.
	ReplaySpectate bool
	// Game hash challenges (challenge.go): where learned answers are kept, whether to learn
	// them from the connected (clean!) clients, and whether to kick on a failed challenge.
	ChallengeFile      string
	LearnChallenges    bool
	KickChallengeFails bool

	// Course layouts (item boxes, checkpoints, respawn points, walls), from the game's
	// files. Item requests are checked against the item boxes; movement checks use the
	// rest. Nil skips those checks.
	Courses map[common.Course]*items.Course
	// Refuse item requests that fail the box check. Otherwise they're only logged.
	EnforceBoxes bool

	// Check reported movement for impossible speeds and teleports (see movement.go).
	CheckMovement bool
	// Strike aids whose movement fails the check. Otherwise it's only logged.
	EnforceMovement bool
	// Vehicle and character stats (offroad speed), from Common.szs. Nil: most lenient.
	Stats *items.Stats
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
	go botLoop()
	go challengeLoop()
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
	a := aidForAddr(addr, data)
	if a == nil {
		logging.Log("Non-aid sent a packet. Address: %v", addr)
		return nil
	}

	if a.kicked || len(data) == 0 {
		return nil
	}
	a.stats.received++
	if data[0] == RacePacketMagic && len(data) >= 0x10 {
		a.stats.lastLengths = [8]byte(data[8:16])
	} else if len(data) <= 8 {
		a.stats.lastControl = string(data[:min(2, len(data))])
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
	case isLoginHashPacket(data):
		handleLoginHash(a, data)
		return nil
	case isGameHashPacket(data):
		handleGameHash(a, data, now)
		return nil
	case isChallengeReply(data):
		handleChallengeReply(a, data)
		return nil
	case isDataChangesPacket(data):
		handleDataChanges(a, data, now)
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
			var ungranted *ungrantedItemError
			if room.config.KickItemCheats && errors.As(err, &ungranted) {
				logging.Log("Aid %d item cheat: %v", a.aid, err)
				kick(a, KickCheating)
				return nil
			}
			if room.config.EnforceItems {
				strike(a, fmt.Sprintf("item violation: %v", err), now)
				return nil
			}
			logging.Log("Aid %d item violation (not enforced): %v", a.aid, err)
			recordFlag(a, "item violation: "+err.Error(), now)
		}
	}

	noteRelayedEvents(a, pkt, now)

	if !a.isRacer && pkt.Select != nil {
		logging.Log("Aid %d sent select!", a.aid)
		a.isRacer = true
	}

	if a.readyForCountdown && pkt.IsCountdownStarted() {
		handleAidStartedCountdown(a)
	}

	trackPlayerState(a, pkt, now)
	if room.config.CheckMovement && a.raceStarted {
		checkFinish(a, pkt, now)
	}
	trackMovement(a, pkt, now)
	recordMirrorSource(a, data, pkt, now)
	recordPackets(a, now)

	a.stats.valid++
	var relayTo []net.Addr
	for _, dest := range *util.GetSendToAids(pkt.Destinations) {
		to := getAid(dest)
		// nil when someone left but wfc-server hasn't told the sender yet.
		// Bots are played by the server, so packets for them stop here.
		if to == nil || to.kicked || to.bot != nil {
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
func trackPlayerState(a *Aid, pkt *RacePacket, now time.Time) {
	if pkt.RaceInfo != nil {
		started := pkt.IsCountdownStarted()
		if started && !a.raceStarted {
			if a.bot == nil && (rec == nil || now.Sub(rec.start) > 20*time.Second) {
				startRecording(pkt.RaceInfo.Course(), now)
			}
			resetItemState(a)
			resetMovement(a)
			resetHitChecks()
			resetProgress(a)
			raceHadFinisher = false
			cloudUsedAt = time.Time{}
			blueThrownAt = time.Time{}
			for _, b := range aids {
				if b.bot != nil {
					resetProgress(b)
				}
			}
			a.raceStartedAt = time.Now()
		}
		a.raceStarted = started
		a.mode = pkt.RaceInfo.GameMode()
		a.course = pkt.RaceInfo.Course()
		for i, p := range a.players {
			p.vehicle, p.character = pkt.RaceInfo.Combo(i)
		}
	}

	for i, rd := range pkt.RaceData {
		if i < len(a.players) {
			a.players[i].rank = rd.Rank
			if rd.HasPosition() {
				recordPosition(a.players[i], rd.WorldPos(), now)
				checkLeafPiles(a, a.players[i], rd.WorldPos(), now)
			}
			if a.raceStarted && room.config.CheckMovement {
				var frame uint32
				if pkt.RaceInfo != nil {
					frame = pkt.RaceInfo.Frame()
				}
				trackProgress(a, i, rd, a.course, frame, now)
			}
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
	lastStats := time.Now()
	for now := range ticker.C {
		mu.Lock()
		if now.Sub(lastStats) >= statsInterval {
			logStats(now.Sub(lastStats))
			lastStats = now
		}
		for _, a := range aids {
			if a == nil || a.kicked || a.bot != nil {
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
	forgetLearnedAddrs(a)

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
