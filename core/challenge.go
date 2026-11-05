package core

// Client challenges are not included in this build.

type challengeState struct{}

// LoadChallenges is a no-op in this build.
func LoadChallenges(path string) error { return nil }

func challengeLoop() {}

func isChallengeReply(data []byte) bool { return false }

// Caller must hold mu.
func handleChallengeReply(a *Aid, data []byte) {}
