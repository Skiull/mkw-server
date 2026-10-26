package talker

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"

	"mkw-server/core"
	"mkw-server/logging"
	"mkw-server/util"
)

// Id: RequestToMKWServer.KickAid (0x03)
// wfc-server asks mkw-server to stop relaying an aid's packets.
type KickAidRequest struct {
	ip   uint32
	port uint16
}

const KickAidRequestLength = 6

func unpackKickAidRequest(msg []byte) (*KickAidRequest, error) {
	if len(msg) != KickAidRequestLength {
		return nil, fmt.Errorf("KickAidRequest isn't %d bytes (%d)", KickAidRequestLength, len(msg))
	}

	return &KickAidRequest{
		ip:   binary.BigEndian.Uint32(msg[0:4]),
		port: binary.BigEndian.Uint16(msg[4:6]),
	}, nil
}

func handleKickAidRequest(req *KickAidRequest) error {
	if req == nil {
		return errors.New("KickAidRequest is nil")
	}

	logging.Log("Handling KickAid. Attempting to kick aid %s", util.FormatIPPort(req.ip, req.port))

	addr := util.CreateUDPAddr(req.ip, req.port)
	return core.KickAid(addr.String(), core.KickRequested)
}

// Id: ResponseFromMKWServer.AidKicked (0x02)
// Layout: type (1), ip (4), port (2), aid (1), reason (1).
func packAidKickedResponse(addr *net.UDPAddr, aid byte, reason core.KickReason) ([]byte, error) {
	ip := addr.IP.To4()
	if ip == nil {
		return nil, fmt.Errorf("aid address %v isn't IPv4", addr)
	}

	b := make([]byte, 9)
	b[0] = AidKicked
	copy(b[1:5], ip)
	binary.BigEndian.PutUint16(b[5:7], uint16(addr.Port))
	b[7] = aid
	b[8] = byte(reason)
	return b, nil
}

// NotifyAidKicked tells wfc-server that mkw-server kicked an aid, so it can disconnect the
// client. A kick wfc-server requested isn't echoed back.
func NotifyAidKicked(addr *net.UDPAddr, aid byte, reason core.KickReason) {
	if reason == core.KickRequested || !TalkerInitialized() {
		return
	}

	msg, err := packAidKickedResponse(addr, aid, reason)
	if err != nil {
		logging.Log("Failed to pack AidKicked: %v", err)
		return
	}

	if err := SendToWFC(msg); err != nil {
		logging.Log("Failed to send AidKicked to wfc-server: %v", err)
	}
}
