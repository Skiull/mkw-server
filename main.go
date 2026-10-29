package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
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
	kickItemCheats := flag.Bool("kick-item-cheats", false, "Kick (as cheating) anyone holding or using an item the server never granted (requires --item-slot)")
	kickRespawnCheats := flag.Bool("kick-respawn-cheats", false, "Kick (as cheating) anyone who respawns away from the course's respawn point (requires --course-dir and --check-movement)")
	gameHashes := flag.String("game-hashes", "", "Comma-separated known-good game hashes (hex, as logged). Clients sending other hashes are logged")
	mutableDataBlocks := flag.String("mutable-data-blocks", "", "Comma-separated StaticR.rel .data block numbers (1 KiB each) that change in normal play, as logged")
	challengeFile := flag.String("challenge-file", "", "File of learned game hash challenge answers (enables challenges)")
	learnChallenges := flag.Bool("learn-challenges", false, "Learn challenge answers from connected clients. Only with a clean client!")
	kickChallengeFails := flag.Bool("kick-challenge-fails", false, "Kick (as cheating) clients that answer a game hash challenge wrong or not at all")
	trustedPlayers := flag.String("trusted-players", "", "Comma-separated friend codes or profile IDs whose clients are trusted: their game hashes, challenge answers and data blocks are learned")
	learnFile := flag.String("learn-file", "", "JSON file for values learned from trusted players (wfc-server reads its login hashes)")
	recordRaw := flag.Bool("record-raw", false, "Diagnostics: also record raw race-state bytes whenever they change")
	recordDir := flag.String("record-dir", "", "Folder to save race recordings in (positions, items, flags per race)")
	kickNoHit := flag.Bool("kick-no-hit", false, "Kick (as cheating) players who don't take a hit the server is sure of (rams, Lightning, Blue Shells, tracked Bananas and Fake Item Boxes)")
	kickDataCheats := flag.Bool("kick-data-cheats", false, "Kick (as cheating) clients whose StaticR.rel .data changed outside --mutable-data-blocks")
	kickHashCheats := flag.Bool("kick-hash-cheats", false, "Kick clients whose game hash isn't in --game-hashes, telling them to update")
	enforceItems := flag.Bool("enforce-items", false, "Drop packets whose items weren't granted by the server (requires --item-slot)")
	courseDir := flag.String("course-dir", "", "Folder with the game's /Race/Course .szs files. Enables item box checks (with --item-slot) and the course-based movement checks (with --check-movement)")
	checkMovement := flag.Bool("check-movement", false, "Check reported movement for impossible speeds and teleports (logged only)")
	enforceMovement := flag.Bool("enforce-movement", false, "Strike aids whose movement fails the check instead of only logging it")
	enforceBoxes := flag.Bool("enforce-boxes", false, "Refuse item requests that fail the item box check instead of only logging them (requires --course-dir)")

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
		KickThreshold:     *kickThreshold,
		KickWindow:        *kickWindow,
		EnforceItems:      *enforceItems,
		KickItemCheats:    *kickItemCheats,
		KickRespawnCheats: *kickRespawnCheats,

		CheckMovement:   *checkMovement || *enforceMovement,
		EnforceMovement: *enforceMovement,
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
		// Common.szs also holds the vehicle and character stats (offroad speeds).
		if stats, err := items.LoadStats(*itemSlotPath); err == nil {
			config.Stats = stats
		} else {
			logging.Log("No vehicle stats in %s (%v): offroad checks assume the most lenient combo", *itemSlotPath, err)
		}
	} else if *enforceItems {
		logging.Log("--enforce-items requires --item-slot")
		os.Exit(1)
	}

	if *gameHashes != "" {
		config.GameHashes = map[uint64]bool{}
		for _, h := range strings.Split(*gameHashes, ",") {
			v, err := strconv.ParseUint(strings.TrimSpace(h), 16, 64)
			if err != nil {
				logging.Log("Bad --game-hashes entry %q: %v", h, err)
				os.Exit(1)
			}
			config.GameHashes[v] = true
		}
		config.KickHashCheats = *kickHashCheats
		logging.Log("%d known game hashes (kick: %v)", len(config.GameHashes), *kickHashCheats)
	}

	config.MutableDataBlocks = map[int]bool{}
	for _, b := range strings.Split(*mutableDataBlocks, ",") {
		if b = strings.TrimSpace(b); b == "" {
			continue
		}
		v, err := strconv.Atoi(b)
		if err != nil {
			logging.Log("Bad --mutable-data-blocks entry %q: %v", b, err)
			os.Exit(1)
		}
		config.MutableDataBlocks[v] = true
	}
	config.KickDataCheats = *kickDataCheats
	config.KickNoHit = *kickNoHit
	config.RecordDir = *recordDir
	config.RecordRaw = *recordRaw
	if *trustedPlayers != "" {
		trusted, err := core.ParseTrustedPlayers(*trustedPlayers)
		if err != nil {
			logging.Log("%v", err)
			os.Exit(1)
		}
		config.TrustedPlayers = trusted
	}
	if *learnFile != "" {
		if err := core.LoadLearned(*learnFile, &config); err != nil {
			logging.Log("Failed to load %s: %v", *learnFile, err)
			os.Exit(1)
		}
		config.LearnFile = *learnFile
	}
	if *challengeFile != "" {
		if err := core.LoadChallenges(*challengeFile); err != nil {
			logging.Log("Failed to load %s: %v", *challengeFile, err)
			os.Exit(1)
		}
		config.ChallengeFile = *challengeFile
		config.LearnChallenges = *learnChallenges
		config.KickChallengeFails = *kickChallengeFails
	}

	if *courseDir != "" {
		courses, errs := items.LoadCourses(*courseDir)
		for _, err := range errs {
			logging.Log("Course files: %v", err)
		}
		config.Courses = courses
		config.EnforceBoxes = *enforceBoxes
		logging.Log("Loaded %d courses (item box enforce: %v)", len(courses), *enforceBoxes)
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
