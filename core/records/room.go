package records

import (
	"encoding/binary"
	"fmt"

	"mkw-server/common"
)

const RoomLen = 0x4

type Room struct {
	action      common.RoomAction
	actionParam uint16
	counter     byte
}

func ParseRoom(data []byte, ctx *Context) (*Room, error) {
	if len(data) != RoomLen {
		return nil, fmt.Errorf("size mismatch (%d != %d)", len(data), RoomLen)
	}

	action := common.RoomAction(data[0x0])
	if !action.IsValid() {
		return nil, fmt.Errorf("invalid action: %d", action)
	}

	actionParam := binary.BigEndian.Uint16(data[0x1:0x3])
	err := action.IsActionValid(actionParam)
	if err != nil {
		return nil, err
	}

	if action == common.Start && !ctx.IsHost {
		return nil, fmt.Errorf("non-host aid %d tried to start the room", ctx.SenderAid)
	}

	// Counter doesn't actually do anything in-game. Don't need to validate it.
	counter := data[0x3]

	return &Room{
		action:      action,
		actionParam: actionParam,
		counter:     counter,
	}, nil
}

func (r *Room) Action() common.RoomAction {
	return r.action
}

func (r *Room) ActionParam() uint16 {
	return r.actionParam
}
