package core

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"mkw-server/logging"
)

const replayFileExt = ".rply"

const crcOffset = 4

func writeReplayFile() error {
	data, err := serializeReplayFile()
	if err != nil {
		return err
	}

	now := time.Now()
	dir, err := replayDir(now)
	if err != nil {
		return err
	}

	path := filepath.Join(dir, now.Format("2006-01-02_15-04-05")+replayFileExt)

	if err := os.WriteFile(path, data, 0664); err != nil {
		return fmt.Errorf("failed to write replay file %s: %w", path, err)
	}
	if err := setUserPermissions(path, 0664); err != nil {
		return fmt.Errorf("failed to set permissions on %s: %w", path, err)
	}

	sizeMB := float64(len(data)) / (1 << 20)
	logging.Log("Wrote replay file %s (%.2f MB)", path, sizeMB)

	return nil
}

// Returns the output directory, creates it if needed.
func replayDir(now time.Time) (string, error) {
	// For now, the directory is relative to the executable.
	// TODO: Make configurable.
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return "", err
	}

	replaysDir := filepath.Join(filepath.Dir(exePath), "replays")
	dateDir := filepath.Join(replaysDir, now.Format("01-02-2006"))

	if err := os.MkdirAll(dateDir, 0775); err != nil {
		return "", err
	}

	for _, d := range []string{replaysDir, dateDir} {
		if err := setUserPermissions(d, 0775); err != nil {
			return "", fmt.Errorf("failed to set permissions on %s: %w", d, err)
		}
	}

	return dateDir, nil
}

// wfc-server spawns the mkw-server executable as a sudo-ed user. This means any files/directories
// it creates need to be sudo-ed modify them, which simply isn't convenient for testing :P.
func setUserPermissions(path string, mode os.FileMode) error {
	uid, uidErr := strconv.Atoi(os.Getenv("SUDO_UID"))
	gid, gidErr := strconv.Atoi(os.Getenv("SUDO_GID"))
	// this shouldn't fail.
	if uidErr == nil && gidErr == nil {
		if err := os.Chown(path, uid, gid); err != nil {
			return err
		}
		return os.Chmod(path, mode)
	}

	return os.Chmod(path, mode|(mode&0070)>>3)
}

var replayBuffer bytes.Buffer
var replayErr error

func write(v any) {
	if replayErr != nil {
		return
	}
	replayErr = binary.Write(&replayBuffer, binary.BigEndian, v)
}

func serializeReplayFile() ([]byte, error) {
	// This shouldn't happen.
	if int(header.AidCount) != len(aidRecords) {
		return nil, fmt.Errorf("Replay header aid counts (%d) don't match # of aid records (%d)", header.AidCount, len(aidRecords))
	}
	if int(header.PlayerCount) != len(playerRecords) {
		return nil, fmt.Errorf("Replay header player counts (%d) don't match # of player records (%d)", header.PlayerCount, len(playerRecords))
	}
	if len(racePacketRecords) != len(aidRecords) {
		return nil, fmt.Errorf("Have %d race packet records but %d aid records", len(racePacketRecords), len(aidRecords))
	}

	replayBuffer = bytes.Buffer{}
	replayErr = nil

	header.CRC32 = 0
	write(header)

	for _, r := range aidRecords {
		write(r.Aid)
		write(r.RaceFinishTime)
		write(r.LocalPlayerCount)
		write(r.Disconnected)
		write(r.AidConfig)

		write(byte(len(r.IdxToAidMap)))
		write(r.IdxToAidMap)

		// Rows are maxRaceFrames long, but nothing after the end time is meaningful.
		for _, row := range r.TimeRecvPackets {
			n := min(int(r.RaceFinishTime), len(row))
			write(uint16(n))
			write(row[:n])
		}
	}

	for _, p := range playerRecords {
		write(p.PlayerId)
		write(p.Aid)
		write(p.Config)
		write(uint16(len(p.RaceFrame)))
		write(p.RaceFrame)
	}

	for _, r := range racePacketRecords {
		if len(r.RacePackets) != int(r.Index.RacePacketCount) {
			return nil, fmt.Errorf("aid %d has %d race packets but its index says %d", r.Aid, len(r.RacePackets), r.Index.RacePacketCount)
		}

		write(r.Aid)
		write(r.Index.TotalSize)
		write(r.Index.RacePacketCount)
		write(uint16(len(r.Index.TimeToOffsetIdx)))
		write(r.Index.TimeToOffsetIdx)
		write(r.Index.PacketOffsets)
		for _, p := range r.RacePackets {
			write(p)
		}
	}

	if replayErr != nil {
		return nil, fmt.Errorf("failed to serialize replay file: %w", replayErr)
	}

	data := replayBuffer.Bytes()
	binary.BigEndian.PutUint32(data[crcOffset:], crc32.ChecksumIEEE(data[crcOffset+4:]))

	return data, nil
}
