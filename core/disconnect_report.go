package core

import (
	"encoding/binary"

	"mkw-server/logging"
)

// Disconnect reports: right before a game disconnects, the client tells the server why, so
// drops on real consoles (whose own log can't be read) show up here.
//
// 'D' kind type 0 code(4) caller(4), big endian. kind is 'C' (setDisconnectInfo) or 'S'
// (scheduleShutdown); caller is the game address that asked for it.
const disconnectReportLen = 12

func isDisconnectReport(data []byte) bool {
	return len(data) == disconnectReportLen && data[0] == 'D' && (data[1] == 'C' || data[1] == 'S')
}

// Caller must hold mu.
func handleDisconnectReport(a *Aid, data []byte) {
	kind := "setDisconnectInfo"
	if data[1] == 'S' {
		kind = "scheduleShutdown"
	}
	code := int32(binary.BigEndian.Uint32(data[4:8]))
	caller := binary.BigEndian.Uint32(data[8:12])
	logging.Log("Aid %d client disconnect report: %s type %d code %d from %#08x (game hash %016x)",
		a.aid, kind, data[2], code, caller, a.gameHash)
}
