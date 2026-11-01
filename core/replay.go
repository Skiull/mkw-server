package core

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"mkw-server/common"
	"mkw-server/core/records"
	"mkw-server/logging"
)

// Race replays. With -record-packets (and -record-dir), every race also gets a packet
// recording next to its .jsonl: <name>.pkt holds each real player's race records as sent,
// and <name>.players.json lists who raced (wfc-server reads it to add one bot per player).
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
// countdown state still come from the live player, like test bots. Items and hits aren't
// replayed: the bots hold nothing.

const replayMagic = "MKWRPL1\n"

// Record types written to .pkt files. Select, User and Room records are left out.
var replayRecordTypes = []common.RecordIdx{common.RaceInfo, common.RaceMode, common.RaceData, common.Item, common.Event}

type packetRecording struct {
	f       *os.File
	w       *bufio.Writer
	base    string
	course  common.Course
	last    map[[2]int]string // aid, record type: last record written.
	players map[byte]bool
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
	f, err := os.Create(base + ".pkt")
	if err != nil {
		logging.Log("Can't create packet recording: %v", err)
		return
	}
	pktRec = &packetRecording{f: f, w: bufio.NewWriterSize(f, 64*1024), base: base, course: course,
		last: map[[2]int]string{}, players: map[byte]bool{}}
	pktRec.w.WriteString(replayMagic)
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
	if !pktRec.players[a.aid] {
		pktRec.players[a.aid] = true
		pktRec.writePlayers()
	}
	for _, t := range replayRecordTypes {
		data := a.mirror.records[t]
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
	for aid := range p.players {
		list = append(list, int(aid))
	}
	sort.Ints(list)
	data, _ := json.Marshal(map[string]any{"course": p.course, "players": list})
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
	raceData  []replayFrame
	frames    [numRecordTypes][]replayFrame // RaceInfo and RaceMode, for spectate replays.
	vehicle   common.Vehicle
	character common.Character
	// Finish time and when they crossed the line (ms since the race started); -1 if they
	// didn't finish.
	finishMs   int64
	finishTime records.FinishTime
}

type replayData struct {
	course  common.Course
	players []*replayPlayer
	start   time.Time // When playback began (the room opened).
	lengthMs int64
}

// Set once at startup from -replay. Read-only after that.
var replay *replayData

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
		var m struct{ Course int }
		if json.Unmarshal(meta, &m) == nil {
			r.course = common.Course(m.Course)
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
		if t == common.RaceInfo || t == common.RaceMode {
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
	sort.Slice(out.players, func(i, j int) bool { return out.players[i].aid < out.players[j].aid })
	return out, nil
}

// replayPlayerFor picks the recorded player a bot plays: the k-th bot by aid plays the k-th
// recorded player. Caller must hold mu.
func replayPlayerFor(bot *Aid) *replayPlayer {
	if replay == nil {
		return nil
	}
	var botAids []int
	for _, a := range aids {
		if a.bot != nil {
			botAids = append(botAids, int(a.aid))
		}
	}
	sort.Ints(botAids)
	for k, aid := range botAids {
		if aid == int(bot.aid) && k < len(replay.players) {
			return replay.players[k]
		}
	}
	return nil
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

// replayAidMap maps recorded aids to the bots playing them. Caller must hold mu.
func replayAidMap() map[byte]byte {
	m := map[byte]byte{}
	for _, a := range aids {
		if a.bot == nil {
			continue
		}
		if p := replayPlayerFor(a); p != nil {
			m[p.aid] = a.aid
		}
	}
	return m
}

// replayPackets builds one packet from every replay bot to every real player. Caller must
// hold mu.
func replayPackets(now time.Time) []outgoing {
	ms := now.Sub(replay.start).Milliseconds()
	aidMap := replayAidMap()
	var out []outgoing
	for _, bot := range aids {
		if bot.bot == nil {
			continue
		}
		p := replayPlayerFor(bot)
		if p == nil {
			continue
		}
		for _, dest := range aids {
			if dest.bot != nil || dest.kicked {
				continue
			}
			out = append(out, outgoing{to: dest.addr, data: buildReplayPacket(bot, p, dest, aidMap, ms)})
			bot.stats.sent++
		}
	}
	return out
}

func buildReplayPacket(bot *Aid, p *replayPlayer, dest *Aid, aidMap map[byte]byte, ms int64) []byte {
	var recs [numRecordTypes][]byte
	if ri := frameAt(p.frames[common.RaceInfo], ms); len(ri) >= 0x24 {
		binary.BigEndian.PutUint16(ri[0xc:], 0) // No lag.
		for i := 0x18; i < 0x24; i++ {
			if a, ok := aidMap[ri[i]]; ok {
				ri[i] = a
			}
		}
		recs[common.RaceInfo] = ri
	}
	if rm := frameAt(p.frames[common.RaceMode], ms); len(rm) >= 0x26 {
		records.PutBits(rm, 41, 4, uint32(bot.aid)) // Player 1 slot.
		rm[0x1c] = 0                                // Not disconnected.
		recs[common.RaceMode] = rm
	}
	recs[common.RaceData] = p.replayRaceData(ms)
	if dest.mirror != nil {
		if u := dest.mirror.records[common.User]; u != nil {
			d := append([]byte{}, u...)
			binary.BigEndian.PutUint32(d[0x0:], 1)
			binary.BigEndian.PutUint64(d[0xa8:], botFriendCode(bot.aid))
			recs[common.User] = d
		}
	}
	var ack byte
	if dest.mirror != nil {
		ack = dest.mirror.itemTimer
	}
	recs[common.Item] = []byte{0, byte(common.NoItem), byte(common.NoItem), 0, 0, 0, ack, 0}
	ev := make([]byte, records.EventSlotCount)
	for i := range ev {
		ev[i] = byte(common.ObjNone)
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
