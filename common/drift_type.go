package common

type DriftType byte

const (
	Manual DriftType = iota
	Auto
)

func (d DriftType) IsValid() bool {
	return d >= Manual && d <= Auto
}
