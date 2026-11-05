package core

import (
	"time"

	"mkw-server/common"
	"mkw-server/core/records"
)

// Not included in this build.

// Caller must hold mu.
func trackShot(a *Aid, s *records.Shot, now time.Time) {}

// Caller must hold mu.
func removeHitItem(a *Aid, obj common.ItemObject, now time.Time) {}
