package records

// MaxAids is the number of client slots (aids) a room can have.
const MaxAids = 12

// NoAid is the value used in aid fields (client slot maps, winning aid, etc.) when unset.
const NoAid byte = 0xff

// Context is what the server knows about the sender of a packet. Records are validated
// against it so a client can't claim to be someone else or to have players it doesn't have.
type Context struct {
	// Aid of the client that sent the packet (already verified against its UDP address).
	SenderAid byte
	// Number of local players on the sender's console (1, or 2 with a guest).
	LocalPlayerCount int
	// Whether the sender is the room's host. Only the host may start a friend room event.
	IsHost bool
	// Reports whether an aid is currently in the room.
	IsAidInRoom func(byte) bool
}

func (c *Context) hasGuest() bool {
	return c.LocalPlayerCount == 2
}

func (c *Context) aidInRoom(aid byte) bool {
	if c.IsAidInRoom == nil {
		return true
	}
	return c.IsAidInRoom(aid)
}
