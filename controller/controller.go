package controller

import (
	"net"

	"mkw-server/core"
	"mkw-server/logging"
	"mkw-server/talker"
)

// Controller is supposed to start and stop the room and the WFC talker
// currently it just feels like a wrapper

// CreateController creates a new controller with the given room address and WFC address
func CreateController(roomAddress string, wfcAddress string, config core.Config) error {
	udpAddr, err := net.ResolveUDPAddr("udp", roomAddress)
	if err != nil {
		logging.Log("Couldn't resolve udp address for room: %s", roomAddress)
		return err
	}

	err = core.InitRoom(udpAddr, config)
	if err != nil {
		logging.Log("Failed to initialize room: %v", err)
		return err
	}

	err = talker.NewWFCTalker(uint16(udpAddr.Port), wfcAddress)
	if err != nil {
		logging.Log("Error creating WFC talker: %v", err)
		return err
	}

	return nil
}

// Start starts the room and the WFC talker
func Start() {
	core.SetKickHandler(talker.NotifyAidKicked)
	talker.Start()
	core.StartRoom()
}

// Close shuts down the room and the WFC talker
func Close() {
	core.CloseRoom()
	talker.Close()
}

// NotifyShutdown notifies the WFC talker that the room is shutting down
func NotifyShutdown() {
	// nothing for now
}
