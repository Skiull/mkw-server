package core

// Not included in this build: ParseTrustedPlayers and LoadLearned are no-ops.

// ParseTrustedPlayers is a no-op in this build.
func ParseTrustedPlayers(list string) (map[uint32]bool, error) { return nil, nil }

// LoadLearned is a no-op in this build.
func LoadLearned(path string, config *Config) error { return nil }

// SetAidProfile records the logged-in profile ID wfc-server gave for an aid.
func SetAidProfile(aidAddr string, profileID uint32) {
	mu.Lock()
	defer mu.Unlock()
	if a := aids[aidAddr]; a != nil {
		a.profileID = profileID
	}
}

func isLoginHashPacket(data []byte) bool { return false }

// Caller must hold mu.
func handleLoginHash(a *Aid, data []byte) {}
