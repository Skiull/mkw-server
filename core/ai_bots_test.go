package core

import (
	"os"
	"testing"
	"time"

	"mkw-server/common"
)

// Runs the real Kinoko build when MKW_AI_BOTS points at it (kinoko/out).
func TestAIDriverRealProcess(t *testing.T) {
	dir := os.Getenv("MKW_AI_BOTS")
	if dir == "" {
		t.Skip("MKW_AI_BOTS not set")
	}
	d, err := startAIDriver(dir, common.LuigiCircuit, aiFunkyKong, aiFlameRunner, 3, 6)
	if err != nil {
		t.Fatal(err)
	}
	defer d.stop()
	if d.countdown == 0 || d.raceStart <= d.countdown || d.rankBit == 0 {
		t.Fatalf("info: countdown %d race %d rank bit %d", d.countdown, d.raceStart, d.rankBit)
	}
	start := time.Now()
	target := d.raceStart + 60*90 // 90 s of racing
	for time.Since(start) < 60*time.Second {
		d.advance(target)
		time.Sleep(20 * time.Millisecond)
		if _, ok := func() (int, bool) { d.mu.Lock(); defer d.mu.Unlock(); return d.frame, d.finished }(); ok {
			break
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.record == nil || d.record[0x27]&0x08 == 0 {
		t.Fatalf("no valid record")
	}
	if !d.finished {
		t.Fatalf("bot didn't finish by frame %d", d.frame)
	}
	t.Logf("finished after %d race frames (%.2fs), took %v", d.finishAt, float64(d.finishAt)/60, time.Since(start))
}
