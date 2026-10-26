package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"mkw-server/controller"
	"mkw-server/core"
	"mkw-server/items"
	"mkw-server/logging"
	"mkw-server/talker"
)

// mkw-server starts when a group is created in wfc-server and a group is
// created in wfc-server when it matches two aids, well before aids are notified they're
// in a group. mkw-server has time to setup and start listeners before aids are notified
func main() {
	err := logging.InitLogFile(talker.SendMessageToWFC)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logging: %v\n", err)
		os.Exit(1)
	}
	defer logging.CloseLogFile()

	// roomAddr is the address aids send to and receive from
	// reason this gets passed in is to help mkw-server manage multiple rooms,
	// could also be useful in the case mkw-server isnt running on the same machine as wfc-serve
	roomAddr := flag.String("room-addr", "", "UDP address (ip:port) the room listens on")

	// wfcAddr is the address of the wfc-server controller
	// This is how mkw-server and wfc-server will communicate
	wfcAddr := flag.String("wfc-addr", "", "TCP address (ip:port) of the WFC controller")

	defaults := core.DefaultConfig()
	kickThreshold := flag.Int("kick-threshold", defaults.KickThreshold, "Kick an aid after this many invalid packets within --kick-window (0 disables kicks)")
	kickWindow := flag.Duration("kick-window", defaults.KickWindow, "Window for counting invalid packets towards a kick")

	// Server-side item decisions need the client patch that asks mkw-server for items.
	// See docs/server-item-decisions.md.
	itemSlotPath := flag.String("item-slot", "", "Path to ItemSlot.bin. Enables server-side item decisions")
	enforceItems := flag.Bool("enforce-items", false, "Drop packets whose items weren't granted by the server (requires --item-slot)")

	flag.Parse()

	// Validate required arguments
	if *roomAddr == "" {
		logging.Log("Missing required argument: --room-addr {ip:port}")
		os.Exit(1)
	}
	if *wfcAddr == "" {
		logging.Log("Missing required argument: --wfc-addr {ip:port}")
		os.Exit(1)
	}

	logging.Log("Starting mkw-server. Room address=%s wfc-server address=%s\n", *roomAddr, *wfcAddr)

	config := core.Config{
		KickThreshold: *kickThreshold,
		KickWindow:    *kickWindow,
		EnforceItems:  *enforceItems,
	}

	if *itemSlotPath != "" {
		slot, err := items.LoadItemSlot(*itemSlotPath)
		if err != nil {
			logging.Log("Failed to load ItemSlot.bin from %s: %v", *itemSlotPath, err)
			os.Exit(1)
		}
		config.ItemEngine, err = items.NewEngine(slot, nil)
		if err != nil {
			logging.Log("Failed to create item engine: %v", err)
			os.Exit(1)
		}
		logging.Log("Server-side item decisions enabled (enforce: %v)", *enforceItems)
	} else if *enforceItems {
		logging.Log("--enforce-items requires --item-slot")
		os.Exit(1)
	}

	err = controller.CreateController(*roomAddr, *wfcAddr, config)

	if err != nil {
		logging.Log("Failed to initialize server: %v", err)
		os.Exit(1)
	}

	// Start the controller (which starts the room and WFC talker)
	controller.Start()

	// mkw-server controls the lifetime of mkw-server and will send a SIGTERM or SIGINT when it wants to close
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	<-sigCh
	logging.Log("Shutting down mkw-server...")
	logging.CloseLogFile()

	controller.NotifyShutdown()

	controller.Close()
}
