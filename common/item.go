package common

// ItemType is an inventory item, as used by ITEM records and ItemSlot.bin rows.
type ItemType byte

const (
	GreenShell ItemType = iota
	RedShell
	Banana
	FakeItemBox
	Mushroom
	TripleMushroom
	BobOmb
	BlueShell
	Lightning
	Star
	GoldenMushroom
	MegaMushroom
	Blooper
	POWBlock
	ThunderCloud
	BulletBill
	TripleGreenShells
	TripleRedShells
	TripleBananas
	UnusedItem
	NoItem

	// Number of rows in an ItemSlot.bin table.
	ItemSlotRowCount = int(TripleBananas) + 1
)

func (i ItemType) IsValid() bool {
	return i <= NoItem && i != UnusedItem
}

// Uses is how many times an item can be used before it's gone. Golden Mushrooms are
// time limited rather than use limited, so they count as one.
func (i ItemType) Uses() int {
	switch i {
	case TripleMushroom, TripleGreenShells, TripleRedShells, TripleBananas:
		return 3
	case NoItem:
		return 0
	default:
		return 1
	}
}

func (i ItemType) String() string {
	names := [...]string{
		"GreenShell", "RedShell", "Banana", "FakeItemBox", "Mushroom", "TripleMushroom",
		"BobOmb", "BlueShell", "Lightning", "Star", "GoldenMushroom", "MegaMushroom",
		"Blooper", "POWBlock", "ThunderCloud", "BulletBill", "TripleGreenShells",
		"TripleRedShells", "TripleBananas", "UnusedItem", "NoItem",
	}
	if int(i) < len(names) {
		return names[i]
	}
	return "Invalid"
}

// ItemObject is the object an item turns into once used, as referenced by EVENT records.
type ItemObject byte

const (
	ObjGreenShell ItemObject = iota
	ObjRedShell
	ObjBanana
	ObjMushroom
	ObjStar
	ObjBlueShell
	ObjLightning
	ObjFakeItemBox
	ObjMegaMushroom
	ObjBobOmb
	ObjBlooper
	ObjPOWBlock
	ObjGoldenMushroom
	ObjBulletBill
	ObjThunderCloud
	_
	ObjNone
)

func (o ItemObject) IsValid() bool {
	return o <= ObjThunderCloud || o == ObjNone
}

// Object returns the object an item creates when used. Triple items create the same
// object as their single counterpart.
func (i ItemType) Object() ItemObject {
	switch i {
	case GreenShell, TripleGreenShells:
		return ObjGreenShell
	case RedShell, TripleRedShells:
		return ObjRedShell
	case Banana, TripleBananas:
		return ObjBanana
	case FakeItemBox:
		return ObjFakeItemBox
	case Mushroom, TripleMushroom:
		return ObjMushroom
	case BobOmb:
		return ObjBobOmb
	case BlueShell:
		return ObjBlueShell
	case Lightning:
		return ObjLightning
	case Star:
		return ObjStar
	case GoldenMushroom:
		return ObjGoldenMushroom
	case MegaMushroom:
		return ObjMegaMushroom
	case Blooper:
		return ObjBlooper
	case POWBlock:
		return ObjPOWBlock
	case ThunderCloud:
		return ObjThunderCloud
	case BulletBill:
		return ObjBulletBill
	default:
		return ObjNone
	}
}

// ItemEventAction is the action half of an EVENT slot.
type ItemEventAction byte

const (
	EventNone ItemEventAction = iota
	EventUsed
	EventShot
	EventHitDragged
	EventHitFree
	EventLose
	EventUnknown6
	EventDrop
)

// EventDataSize is the size of an event's data, or -1 when it depends on the item (Shot).
func (a ItemEventAction) EventDataSize() int {
	switch a {
	case EventNone:
		return 0
	case EventUsed:
		return 3
	case EventShot:
		return -1
	case EventHitDragged, EventDrop:
		return 20
	case EventHitFree, EventLose:
		return 5
	case EventUnknown6:
		return 4
	default:
		return -1
	}
}

// IsOwnItem reports whether the action is the sender using an item it holds, as opposed
// to interacting with someone else's.
func (a ItemEventAction) IsOwnItem() bool {
	return a == EventUsed || a == EventShot
}
