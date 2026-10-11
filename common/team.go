package common

type Team byte

const (
	Red Team = iota
	Blue
	None
)

func (t Team) IsValid() bool {
	return t >= Red && t <= None
}
