package common

type RegionLine byte

const (
	Japan RegionLine = iota
	Americas
	Eur
	Aus
	Taiwan
	Korea
)

func (r RegionLine) IsValid() bool {
	return r >= Japan && r <= Korea
}
