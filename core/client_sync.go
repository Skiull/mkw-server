package core

import (
	"encoding/binary"
	"time"

	"mkw-server/logging"
)

const readyPacket = "RE"
const startPacket = "ST"
const readyAckPacket = "RA"
const pingPacket = "PI"
const pongPacket = "PO"
const pingTimePacket = "PT"

func isReadyPacket(data []byte) bool {
	return string(data) == readyPacket
}

// Check RH1.timeSinceCountdown. This will be non-zero if the countdown started.
// TODO: Rewrite when Race packet parsing is implemented.
func hasCountdownStarted(data []byte) bool {
	// Need to check packet is at least len(Header) + len(RH1) (0x10 + 0x28).
	if len(data) < 0x38 {
		return false
	}

	// Offset 0x9 is the Select Record record size field in the Header record.
	// Need to check that the size is exactly 0x28.
	if data[0x9] != 0x28 {
		return false
	}

	// timeSinceCountdown is the first field in RH1.
	timeSinceCountdown := binary.BigEndian.Uint32(data[0x10:0x14])
	return timeSinceCountdown != 0
}

func handleAidReady(a *Aid) {
	logging.Log("Aid %d sent a ready", a.aid)
	a.readyForCountdown = true

	// Send them an ack. Aid will stop sending ready packets if received.
	sendReadyAck(a)

	tryUpdateRoomCountdownState()
}

func sendReadyAck(a *Aid) {
	room.conn.WriteTo([]byte(readyAckPacket), a.addr)
}

func handleAidStartedCountdown(a *Aid) {
	logging.Log("Aid %d started the countdown", a.aid)
	a.readyForCountdown = false

	tryUpdateRoomCountdownState()
}

func tryUpdateRoomCountdownState() {
	firstAid := false
	unanimousState := false

	for addr, a := range aids {
		if addr == "" || a == nil {
			continue
		}

		// In public rooms, aids from the waiting list are added before the countdown starts.
		// These aids are never in the race and can't influence countdown logic.
		if !a.isRacer {
			continue
		}

		if !firstAid {
			unanimousState = a.readyForCountdown
			firstAid = true
		} else if a.readyForCountdown != unanimousState {
			// Return early if anyone disagrees
			return
		}
	}

	if !firstAid || unanimousState == room.countdownState {
		return
	}

	// All aids must send their config for the countdown to start.
	if unanimousState && !haveAllAidsSentConfig() {
		return
	}

	room.countdownState = unanimousState

	if room.countdownState {
		logging.Log("All aids sent ready and room's countdown state updated. Room's countdown should start!")
		resetRace()
		beginSendStart()
	} else {
		logging.Log("All aids have started the countdown locally. Room's countdown state reset")
		resetAllAidsLatency()
	}
}

func beginSendStart() {
	maxLatency := getMaxLatency()
	for _, a := range aids {
		if a == nil || !a.isRacer {
			continue
		}

		// Start a goroutine for each aid to send with a delay
		go func(aid *Aid) {
			delay := maxLatency - aid.latency
			logging.Log("Sending aid %d Start after %v time", aid.aid, delay)
			time.Sleep(delay)

			_, err := room.conn.WriteTo([]byte(startPacket), aid.addr)
			if err != nil {
				logging.Log("Error sending Start to aid %d: %v", aid.aid, err)
				return
			}

		}(a)
	}
}

func sendPing(a *Aid) {
	a.lastPingSent = time.Now()
	room.conn.WriteTo([]byte(pingPacket), a.addr)
}

func isPongPacket(data []byte) bool {
	return string(data) == pongPacket
}

func handlePongPacket(a *Aid) {
	elapsed := time.Since(a.lastPingSent)

	// Protect against any giant elapsed times. Hacky, but large ping can mess with the countdown.
	if elapsed > 3*time.Second {
		logging.Log("Aid %d sent a pong with unacceptable elapsed time (%v)", a.aid, elapsed)
		return
	}

	a.latencySum += time.Since(a.lastPingSent)
	a.latencyCount++
	a.latency = a.latencySum / time.Duration(a.latencyCount)
	sendPingTimePacket(a)
}

func resetAllAidsLatency() {
	for _, a := range aids {
		if a == nil || !a.isRacer {
			continue
		}
		logging.Log("Aid %d's average latency: %v (samples: %d)", a.aid, a.latency, a.latencyCount)

		a.latencyCount = 0
		a.latencySum = 0
		a.latency = 0
	}
}

func getMaxLatency() time.Duration {
	var maxLatency time.Duration
	for _, a := range aids {
		if a == nil || !a.isRacer {
			continue
		}

		if maxLatency < a.latency {
			maxLatency = a.latency
		}
	}
	return maxLatency
}

func sendPingTimePacket(a *Aid) {
	ms := a.latency.Milliseconds()
	subMs := (a.latency.Nanoseconds() % 1_000_000)
	subMsDigits := subMs / 10_000

	msDigits := [3]byte{
		byte((ms / 100) % 10),
		byte((ms / 10) % 10),
		byte(ms % 10),
	}
	nsDigits := [2]byte{
		byte(subMsDigits / 10),
		byte(subMsDigits % 10),
	}

	// Replace leading zeros with 11. The HUD hides any digit with a value greater than 10.
	for i := 0; i < len(msDigits)-1; i++ {
		if msDigits[i] == 0 {
			msDigits[i] = 11
		} else {
			break
		}
	}

	packet := []byte(pingTimePacket)
	packet = append(packet, msDigits[:]...)
	packet = append(packet, nsDigits[:]...)

	_, err := room.conn.WriteTo(packet, a.addr)
	if err != nil {
		logging.Log("Error sending PingTime to aid %d: %v", a.aid, err)
	}
}
