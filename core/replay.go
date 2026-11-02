package core

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mkw-server/common"
	"mkw-server/core/records"
	"mkw-server/logging"
)

// Race replays. With -record-packets (and -record-dir), every race also gets a packet
// recording next to its .jsonl: <name>.pkt holds each real player's race records as sent,
// and <name>.players.json lists who raced and their profile ids (wfc-server reads it to pick
// a race the watcher was in and add one bot per player). Only the newest maxPacketRecordings
// are kept, and a race isn't recorded when the disk is nearly full.
//
// .pkt layout: the magic, then frames of
//
//	u32 ms since the race started, u8 aid, u8 record type (common.RecordIdx), u16 length, data
//
// A record is only written when it differs from that player's previous one of its type.
//
// With -replay <file.pkt>, the room's bots play the recorded players back instead of
// mirroring the live player: bot k (by aid) drives player k's recorded RaceData, timed from
// the live race's countdown start, with that player's character and vehicle. Lobby and
// countdown state still come from the live player, like test bots.

const replayMagic = "MKWRPL1\n"

// Record types written to .pkt files. Room records are left out. User (Miis, names,
// location) and Select (room phase, course, player map) are the player's latest ones, which
// may have been sent before the race.
var replayRecordTypes = []common.RecordIdx{common.User, common.RoomSelect, common.RaceInfo, common.RaceMode, common.RaceData, common.Item, common.Event}

// How many packet recordings to keep, and the free disk space needed to start one (a race
// is a few MB).
const (
	maxPacketRecordings = 12
	minFreeDiskBytes    = 200 << 20
)

type packetRecording struct {
	f       *os.File
	w       *bufio.Writer
	base    string
	course  common.Course
	last    map[[2]int]string // aid, record type: last record written.
	players map[byte]uint32   // aid: profile id (0 if unknown).
}

// Guarded by mu. Nil unless -record-packets is on and a race is being recorded.
var pktRec *packetRecording

// startPacketRecording opens the packet recording for a race. base is the .jsonl path
// without its extension. Caller must hold mu.
func startPacketRecording(base string, course common.Course) {
	stopPacketRecording()
	if !room.config.RecordPackets || room.config.ReplayFile != "" {
		return
	}
	if free, err := diskFree(filepath.Dir(base)); err == nil && free < minFreeDiskBytes {
		logging.Log("Not recording packets: only %d MB free", free>>20)
		return
	}
	f, err := os.Create(base + ".pkt")
	if err != nil {
		logging.Log("Can't create packet recording: %v", err)
		return
	}
	pktRec = &packetRecording{f: f, w: bufio.NewWriterSize(f, 64*1024), base: base, course: course,
		last: map[[2]int]string{}, players: map[byte]uint32{}}
	pktRec.w.WriteString(replayMagic)
	prunePacketRecordings(room.config.RecordDir, maxPacketRecordings-1)
}

// prunePacketRecordings deletes all but the newest keep packet recordings (and their player
// lists) under dir. The race .jsonl files stay.
func prunePacketRecordings(dir string, keep int) {
	files, _ := filepath.Glob(filepath.Join(dir, "*", "*.pkt"))
	if len(files) <= keep {
		return
	}
	// Names start with the date folder and time, so they sort oldest first.
	sort.Strings(files)
	for _, f := range files[:len(files)-keep] {
		os.Remove(f)
		os.Remove(strings.TrimSuffix(f, ".pkt") + ".players.json")
	}
}

// stopPacketRecording finishes the packet recording. Caller must hold mu.
func stopPacketRecording() {
	if pktRec == nil {
		return
	}
	pktRec.w.Flush()
	pktRec.f.Close()
	pktRec = nil
}

// recordPackets writes the records a real player just sent. Caller must hold mu.
func recordPackets(a *Aid, now time.Time) {
	if pktRec == nil || rec == nil || a.bot != nil || a.mirror == nil || !a.raceStarted {
		return
	}
	ms := rec.ms(now)
	if ms < 0 {
		return
	}
	if profile, ok := pktRec.players[a.aid]; !ok || profile != a.profileID {
		pktRec.players[a.aid] = a.profileID
		pktRec.writePlayers()
	}
	for _, t := range replayRecordTypes {
		data := a.mirror.records[t]
		switch t {
		case common.User:
			data = a.mirror.lastUser
		case common.RoomSelect:
			data = a.mirror.lastSelect
		}
		if len(data) == 0 {
			continue
		}
		key := [2]int{int(a.aid), int(t)}
		if pktRec.last[key] == string(data) {
			continue
		}
		pktRec.last[key] = string(data)
		var hdr [8]byte
		binary.BigEndian.PutUint32(hdr[0:], uint32(ms))
		hdr[4], hdr[5] = a.aid, byte(t)
		binary.BigEndian.PutUint16(hdr[6:], uint16(len(data)))
		pktRec.w.Write(hdr[:])
		pktRec.w.Write(data)
	}
	// The room process is killed when the room closes, so don't sit on much.
	if pktRec.w.Buffered() > 32*1024 {
		pktRec.w.Flush()
	}
}

func (p *packetRecording) writePlayers() {
	var list []int
	profiles := map[string]uint32{}
	for aid, profile := range p.players {
		list = append(list, int(aid))
		profiles[fmt.Sprint(aid)] = profile
	}
	sort.Ints(list)
	data, _ := json.Marshal(map[string]any{"course": p.course, "players": list, "profiles": profiles})
	os.WriteFile(p.base+".players.json", data, 0o644)
}

// replayFrame is one recorded record.
type replayFrame struct {
	ms   int64
	data []byte
}

// replayPlayer is one recorded racer.
type replayPlayer struct {
	aid       byte
	profile   uint32 // Profile id, from the player list (0 if unknown).
	raceData  []replayFrame
	frames    [numRecordTypes][]replayFrame // Other record types, for spectate replays.
	vehicle   common.Vehicle
	character common.Character
	// Finish time and when they crossed the line (ms since the race started); -1 if they
	// didn't finish.
	finishMs   int64
	finishTime records.FinishTime
}

type replayData struct {
	course   common.Course
	players  []*replayPlayer
	start    time.Time // When playback began (the room opened).
	lengthMs int64
}

// Set once at startup from -replay. Read-only after that.
var replay *replayData

// Set once the replay is over. Guarded by mu.
var replayEnded bool

// LoadReplay reads a .pkt recording for -replay.
func LoadReplay(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r, err := parseReplay(bufio.NewReader(f))
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if meta, err := os.ReadFile(strings.TrimSuffix(path, ".pkt") + ".players.json"); err == nil {
		var m struct {
			Course   int
			Profiles map[string]uint32
		}
		if json.Unmarshal(meta, &m) == nil {
			r.course = common.Course(m.Course)
			for _, p := range r.players {
				p.profile = m.Profiles[fmt.Sprint(p.aid)]
			}
		}
	}
	r.start = time.Now()
	replay = r
	logging.Log("Loaded replay %s: course %d, %d players", path, r.course, len(r.players))
	return nil
}

func parseReplay(r io.Reader) (*replayData, error) {
	magic := make([]byte, len(replayMagic))
	if _, err := io.ReadFull(r, magic); err != nil || string(magic) != replayMagic {
		return nil, errors.New("not a packet recording")
	}
	byAid := map[byte]*replayPlayer{}
	out := &replayData{course: common.Course(0xff)}
	var hdr [8]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			break // End, or a frame cut off when the room closed.
		}
		ms := int64(binary.BigEndian.Uint32(hdr[0:]))
		aid, t := hdr[4], common.RecordIdx(hdr[5])
		data := make([]byte, binary.BigEndian.Uint16(hdr[6:]))
		if _, err := io.ReadFull(r, data); err != nil {
			break
		}
		out.lengthMs = max(out.lengthMs, ms)
		p := byAid[aid]
		if p == nil {
			p = &replayPlayer{aid: aid, finishMs: -1, vehicle: common.StandardKartM, character: common.Mario}
			byAid[aid] = p
			out.players = append(out.players, p)
		}
		if t != common.RaceData {
			p.frames[t] = append(p.frames[t], replayFrame{ms: ms, data: data})
		}
		switch t {
		case common.RaceData:
			if len(data) >= records.RaceDataLen {
				p.raceData = append(p.raceData, replayFrame{ms: ms, data: data[:records.RaceDataLen]})
			}
		case common.RaceInfo:
			if len(data) > 0xf && data[0xe] != byte(common.RaceInfoDefaultVehicle) {
				p.vehicle, p.character = common.Vehicle(data[0xe]), common.Character(data[0xf])
			}
			if info, err := records.ParseRaceInfo(data); err == nil && out.course == 0xff {
				out.course = info.Course()
			}
		case common.RaceMode:
			if p.finishMs >= 0 || len(data) < 14 {
				continue
			}
			// VS: player 1's finish time is bits 45-66 (see botRaceMode).
			ft := records.GetBits(data, 45, 22)
			if ft != 0 {
				p.finishMs = ms
				p.finishTime = records.FinishTime{Minutes: byte(ft >> 16), Seconds: byte(ft >> 10 & 0x3f), Milliseconds: uint16(ft & 0x3ff)}
			}
		}
	}
	if len(out.players) == 0 {
		return nil, errors.New("no players recorded")
	}
	// A recording runs on past the race (results, the next vote) until the next race starts.
	// The replay ends a little after the last finish instead, or the bots would take the
	// watcher into a vote.
	lastFinish := int64(-1)
	for _, p := range out.players {
		lastFinish = max(lastFinish, p.finishMs)
	}
	if lastFinish >= 0 {
		out.lengthMs = min(out.lengthMs, lastFinish+replayAfterFinish.Milliseconds())
	}
	sort.Slice(out.players, func(i, j int) bool { return out.players[i].aid < out.players[j].aid })
	return out, nil
}

// replaySeat is what a bot plays: a recorded player, and maybe a second one as its guest.
type replaySeat struct {
	main, guest *replayPlayer
}

// replaySeats assigns recorded players to bots: the k-th bot by aid plays the k-th recorded
// player. A room has room for 11 bots next to the watcher, so in a 12-player race the
// watcher's own recorded player rides as the first bot's guest (a second local player, like
// split screen) instead of being left out. Caller must hold mu.
func replaySeats() map[byte]replaySeat {
	seats := map[byte]replaySeat{}
	if replay == nil {
		return seats
	}
	var bots []int
	watchers := map[uint32]bool{}
	for _, a := range aids {
		if a.bot != nil {
			bots = append(bots, int(a.aid))
		} else if a.profileID != 0 {
			watchers[a.profileID] = true
		}
	}
	sort.Ints(bots)
	players := replay.players
	var guest *replayPlayer
	if len(players) > len(bots) {
		for i, p := range players {
			if p.profile != 0 && watchers[p.profile] {
				guest = p
				players = append(append([]*replayPlayer{}, players[:i]...), players[i+1:]...)
				break
			}
		}
	}
	for k, aid := range bots {
		if k >= len(players) {
			break
		}
		seat := replaySeat{main: players[k]}
		if k == 0 && room.config.ReplaySpectate {
			seat.guest = guest
		}
		seats[byte(aid)] = seat
	}
	return seats
}

// replayPlayerFor is the recorded player a bot plays. Caller must hold mu.
func replayPlayerFor(bot *Aid) *replayPlayer {
	return replaySeats()[bot.aid].main
}

// replayRaceData is the recorded RaceData at ms into the race.
func (p *replayPlayer) replayRaceData(ms int64) []byte {
	return frameAt(p.raceData, ms)
}

// frameAt is a copy of the last frame at or before ms (the first one before that).
func frameAt(frames []replayFrame, ms int64) []byte {
	if len(frames) == 0 {
		return nil
	}
	i := sort.Search(len(frames), func(i int) bool { return frames[i].ms > ms })
	if i > 0 {
		i--
	}
	return append([]byte{}, frames[i].data...)
}

// Spectate replays: the bots don't mirror anyone. They own the room (wfc-server makes a bot
// the host and adds the watcher after them) and play the recorded race from the moment the
// room opened, so the watcher joins a race already in progress and the game spectates it.
// Each bot sends its recorded player's RaceInfo, RaceMode and RaceData, with aids changed
// from the recorded ones to the bots'.

// mapReplayAid changes a recorded aid to the bot playing it. Aids nobody plays (test bots
// in the recorded race, which aren't recorded) become empty: left as they were, one could
// be the watcher's aid, and the watcher's game would think it's racing and drop out.
func mapReplayAid(aidMap map[byte]byte, aid byte) byte {
	if aid == records.NoAid {
		return aid
	}
	if a, ok := aidMap[aid]; ok {
		return a
	}
	return records.NoAid
}

// replayAidMap maps recorded aids to the bots playing them.
func replayAidMap(seats map[byte]replaySeat) map[byte]byte {
	m := map[byte]byte{}
	for aid, seat := range seats {
		m[seat.main.aid] = aid
		if seat.guest != nil {
			m[seat.guest.aid] = aid
		}
	}
	return m
}

// How long the room stays open after the replay ends.
// Then mkw-server exits and wfc-server closes the room: the bots can't run the results
// screen or another race, and the game would sit there forever.
const replayEndLinger = 2 * time.Second

// How long a replay runs after the last player finished.
const replayAfterFinish = 8 * time.Second

// replayPackets builds one packet from every replay bot to every real player. Caller must
// hold mu.
func replayPackets(now time.Time) []outgoing {
	ms := now.Sub(replay.start).Milliseconds()
	if ms > replay.lengthMs+replayEndLinger.Milliseconds() && !replayEnded {
		replayEnded = true
		logging.Log("Replay finished. Closing the room")
		go func() {
			time.Sleep(time.Second) // Let the log flush.
			os.Exit(0)
		}()
	}
	seats := replaySeats()
	aidMap := replayAidMap(seats)
	var out []outgoing
	for _, bot := range aids {
		if bot.bot == nil {
			continue
		}
		seat, ok := seats[bot.aid]
		if !ok {
			continue
		}
		for _, dest := range aids {
			if dest.bot != nil || dest.kicked {
				continue
			}
			out = append(out, outgoing{to: dest.addr, data: buildReplayPacket(bot, seat, dest, aidMap, ms, now)})
			bot.stats.sent++
		}
	}
	return out
}

// Offsets in the User record (records/user.go).
const (
	userPresentMiis = 0x0
	userMii1        = 0x8
	userMii2        = 0x54
	userMiiLen      = 0x4c
	userFriendCode  = 0xa8
)

func buildReplayPacket(bot *Aid, seat replaySeat, dest *Aid, aidMap map[byte]byte, ms int64, now time.Time) []byte {
	p, g := seat.main, seat.guest
	var recs [numRecordTypes][]byte
	if ri := frameAt(p.frames[common.RaceInfo], ms); len(ri) >= 0x24 {
		binary.BigEndian.PutUint16(ri[0xc:], 0) // No lag.
		for i := 0x18; i < 0x24; i++ {
			ri[i] = mapReplayAid(aidMap, ri[i])
		}
		if g != nil {
			// Player 2's vehicle and character.
			if gri := frameAt(g.frames[common.RaceInfo], ms); len(gri) >= 0x10 {
				ri[0x10], ri[0x11] = gri[0xe], gri[0xf]
			}
		}
		recs[common.RaceInfo] = ri
	}
	// The room's Select record as recorded (phase, course, player map), so a watcher joining
	// sees a race in progress instead of the character select. Aids are the bots', and the
	// clock fields are filled in like a live player's.
	if sel := frameAt(p.frames[common.RoomSelect], ms); len(sel) == records.SelectLen {
		epoch := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
		binary.BigEndian.PutUint64(sel[0x0:], uint64(now.Sub(epoch).Milliseconds()))
		var receiver uint64
		if m := dest.mirror; m != nil && m.timeSender != 0 {
			receiver = m.timeSender + uint64(now.Sub(m.timeAt).Milliseconds())
		}
		binary.BigEndian.PutUint64(sel[0x8:], receiver)
		for i := 0x28; i < 0x34; i++ {
			sel[i] = mapReplayAid(aidMap, sel[i])
		}
		if a, ok := aidMap[sel[0x36]]; ok {
			sel[0x36] = a // Winning vote.
		}
		if g != nil {
			if gsel := frameAt(g.frames[common.RoomSelect], ms); len(gsel) == records.SelectLen {
				copy(sel[0x18:0x20], gsel[0x10:0x18]) // The guest's spot, points, combo and rank,
				sel[0x1e] = sel[0x16]                 // voting with player 1.
			}
		}
		recs[common.RoomSelect] = sel
	}
	// The player slots in RaceMode are player ids, which stay as recorded.
	if rm := frameAt(p.frames[common.RaceMode], ms); len(rm) >= 0x26 {
		rm[0x1c] = 0 // Not disconnected.
		if g != nil {
			if grm := frameAt(g.frames[common.RaceMode], ms); len(grm) >= 0x26 {
				records.PutBits(rm, 39, 2, 2)                              // Two local players.
				records.PutBits(rm, 80, 4, records.GetBits(grm, 41, 4))    // Player 2's slot,
				records.PutBits(rm, 84, 22, records.GetBits(grm, 45, 22))  // finish time
				records.PutBits(rm, 106, 13, records.GetBits(grm, 67, 13)) // and players ahead.
				copy(rm[0x20:0x22], grm[0x1e:0x20])                        // Slow frames.
			}
		}
		recs[common.RaceMode] = rm
	}
	recs[common.RaceData] = p.replayRaceData(ms)
	if g != nil && recs[common.RaceData] != nil {
		if grd := g.replayRaceData(ms); grd != nil {
			recs[common.RaceData] = append(recs[common.RaceData], grd...)
		}
	}
	// The recorded player's Mii, name and location. The friend code is the bot's: the
	// watcher's own one would clash with theirs.
	user := frameAt(p.frames[common.User], ms)
	if len(user) != records.UserLen && dest.mirror != nil && dest.mirror.lastUser != nil {
		user = append([]byte{}, dest.mirror.lastUser...)
	}
	if len(user) == records.UserLen {
		binary.BigEndian.PutUint32(user[userPresentMiis:], 1)
		if g != nil {
			if gu := frameAt(g.frames[common.User], ms); len(gu) == records.UserLen {
				copy(user[userMii2:userMii2+userMiiLen], gu[userMii1:userMii1+userMiiLen])
				binary.BigEndian.PutUint32(user[userPresentMiis:], 0b11)
			}
		}
		binary.BigEndian.PutUint64(user[userFriendCode:], botFriendCode(bot.aid))
		recs[common.User] = user
	}
	// Recorded items and item events (throws, drops, hits), so the watcher's game shows them.
	// ack_timer echoes the watcher's own item timer, as bots do.
	var ack byte
	if dest.mirror != nil {
		ack = dest.mirror.itemTimer
	}
	item := frameAt(p.frames[common.Item], ms)
	if len(item) < records.ItemLen {
		item = []byte{0, byte(common.NoItem), byte(common.NoItem), 0, 0, 0, 0, 0}
	}
	item[6] = ack
	recs[common.Item] = item
	ev := frameAt(p.frames[common.Event], ms)
	if len(ev) == 0 {
		ev = make([]byte, records.EventSlotCount)
		for i := range ev {
			ev[i] = byte(common.ObjNone)
		}
	}
	recs[common.Event] = ev
	return encodeRacePacket(bot.aid, dest.aid, recs)
}

// replayFinished reports a replayed player's finish once the replay has reached it.
func (p *replayPlayer) replayFinished(ms int64) (records.FinishTime, bool) {
	if p.finishMs < 0 || ms < p.finishMs {
		return records.FinishTime{}, false
	}
	return p.finishTime, true
}

// replayMs is how far into the replay the live race is.
func replayMs(ref *Aid, now time.Time) int64 {
	if ref == nil || !ref.raceStarted || ref.raceStartedAt.IsZero() {
		return 0
	}
	return now.Sub(ref.raceStartedAt).Milliseconds()
}
