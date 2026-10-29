package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"mkw-server/common"
	"mkw-server/core/records"
	"mkw-server/logging"
)

// Race recordings, for reviewing suspicious races. With -record-dir, each race is written
// as JSON Lines to <dir>/<date>/<time>_course<id>_<room port>.jsonl:
//
//	{"type":"race","start":"<RFC3339>","course":11,"room":"0.0.0.0:26774"}
//	{"type":"player","aid":0,"p":0,"friendCode":"3699-5411-5651","vehicle":23,"character":22}
//	{"type":"pos","t":1234,"aid":0,"p":0,"x":1.5,"y":2,"z":3,"speed":88}
//	{"type":"item","t":1234,"aid":0,"p":0,"event":"granted","item":"Banana"}
//	{"type":"flag","t":1234,"aid":0,"msg":"movement violation ..."}
//	{"type":"end","t":185000}
//
// t is milliseconds since the race started. Positions are written at most every
// recordPosInterval per player. Bots aren't recorded. The track outline for the viewer is
// in <dir>/courses/<id>.json (see writeCourseOutline).

const recordPosInterval = 100 * time.Millisecond

type recording struct {
	f       *os.File
	start   time.Time
	course  common.Course
	players map[[2]int]bool // aid, local player: "player" line written.
	lastPos map[[2]int]time.Time
	lastAt  time.Time
	lastRaw map[[2]int]string // -record-raw: last state bytes per player.
}

// Guarded by mu.
var rec *recording

// startRecording begins a new race recording. Caller must hold mu.
func startRecording(course common.Course, now time.Time) {
	dir := room.config.RecordDir
	if dir == "" {
		return
	}
	stopRecording(now)
	day := filepath.Join(dir, now.Format("2006-01-02"))
	if err := os.MkdirAll(day, 0o755); err != nil {
		logging.Log("Can't create recording folder: %v", err)
		return
	}
	port := 0
	if room.addr != nil {
		port = room.addr.Port
	}
	name := filepath.Join(day, fmt.Sprintf("%s_course%d_%d.jsonl", now.Format("150405"), course, port))
	f, err := os.Create(name)
	if err != nil {
		logging.Log("Can't create recording: %v", err)
		return
	}
	rec = &recording{f: f, start: now, course: course,
		players: map[[2]int]bool{}, lastPos: map[[2]int]time.Time{}, lastAt: now, lastRaw: map[[2]int]string{}}
	rec.write(map[string]any{"type": "race", "start": now.Format(time.RFC3339), "course": course, "room": room.addr.String()})
	writeCourseOutline(dir, course)
}

// stopRecording finishes the current recording. Caller must hold mu.
func stopRecording(now time.Time) {
	if rec == nil {
		return
	}
	rec.write(map[string]any{"type": "end", "t": rec.ms(now)})
	rec.f.Close()
	rec = nil
}

func (r *recording) ms(now time.Time) int64 { return now.Sub(r.start).Milliseconds() }

func (r *recording) write(v map[string]any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	// Unbuffered: the room process is killed when the room closes.
	r.f.Write(append(data, '\n'))
}

func recordable(a *Aid) bool { return rec != nil && a != nil && a.bot == nil }

// recordPos writes a player's position, rate limited. Caller must hold mu.
func recordPos(a *Aid, i int, p *Player, rd *records.RaceData, now time.Time) {
	if !recordable(a) || !rd.HasPosition() {
		return
	}
	key := [2]int{int(a.aid), i}
	if !rec.players[key] {
		rec.players[key] = true
		rec.write(map[string]any{"type": "player", "aid": a.aid, "p": i,
			"friendCode": formatFriendCode(a.friendCode), "vehicle": p.vehicle, "character": p.character})
	}
	// Diagnostics (-record-raw): the state bytes 0x20-0x3f whenever they change, with the
	// speed, to find which bits mark boosts.
	if room.config.RecordRaw {
		raw := fmt.Sprintf("%x", rd.Raw()[0x20:])
		if raw != rec.lastRaw[key] {
			rec.lastRaw[key] = raw
			rec.write(map[string]any{"type": "raw", "t": rec.ms(now), "aid": a.aid, "p": i, "b20": raw, "speed": round1(p.move.lastSpeed)})
		}
	}
	if now.Sub(rec.lastPos[key]) < recordPosInterval {
		return
	}
	rec.lastPos[key] = now
	rec.lastAt = now
	pos := rd.WorldPos()
	rec.write(map[string]any{"type": "pos", "t": rec.ms(now), "aid": a.aid, "p": i,
		"x": round1(pos[0]), "y": round1(pos[1]), "z": round1(pos[2]), "speed": round1(p.move.lastSpeed)})
}

// recordItem notes an item event (granted, used, threw, hit). Caller must hold mu.
func recordItem(a *Aid, i int, event string, item fmt.Stringer, now time.Time) {
	if !recordable(a) {
		return
	}
	rec.write(map[string]any{"type": "item", "t": rec.ms(now), "aid": a.aid, "p": i, "event": event, "item": item.String()})
}

// recordFlag notes a violation, failed check or kick. Caller must hold mu.
func recordFlag(a *Aid, msg string, now time.Time) {
	if !recordable(a) {
		return
	}
	rec.write(map[string]any{"type": "flag", "t": rec.ms(now), "aid": a.aid, "msg": msg})
}

func round1(f float32) float64 { return float64(int64(f*10)) / 10 }

func formatFriendCode(fc uint64) string {
	if fc == 0 {
		return ""
	}
	s := fmt.Sprintf("%012d", fc)
	return s[0:4] + "-" + s[4:8] + "-" + s[8:12]
}

// writeCourseOutline saves a course's checkpoint edges and item boxes for the viewer, once.
func writeCourseOutline(dir string, course common.Course) {
	path := filepath.Join(dir, "courses", fmt.Sprintf("%d.json", course))
	if _, err := os.Stat(path); err == nil {
		return
	}
	c := room.config.Courses[course]
	if c == nil {
		return
	}
	out := map[string]any{"course": course}
	if c.Checkpoints != nil {
		var cps []map[string]any
		for _, cp := range c.Checkpoints.List {
			cps = append(cps, map[string]any{"left": cp.Left, "right": cp.Right, "next": cp.Next, "type": cp.Type})
		}
		out["checkpoints"] = cps
	}
	var boxes [][3]float32
	for _, b := range c.Boxes {
		boxes = append(boxes, b.Pos)
	}
	out["itemBoxes"] = boxes
	out["respawns"] = c.Respawns
	data, err := json.Marshal(out)
	if err != nil {
		return
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, data, 0o644)
}
