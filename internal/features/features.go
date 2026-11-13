package features

import "sync/atomic"

const (
	Clipboard = 0
	Files     = 1
	Drag      = 2
	Follows   = 8
)

type Set struct {
	bits atomic.Int32
}

func (s *Set) Turn(bit int, on bool) int32 {
	mask := int32(1) << bit
	for {
		before := s.bits.Load()
		after := before &^ mask
		if on {
			after = before | mask
		}
		if s.bits.CompareAndSwap(before, after) {
			return after
		}
	}
}

func (s *Set) Value() int32 {
	return s.bits.Load()
}

func (s *Set) Has(bit int) bool {
	return s.Value()&(int32(1)<<bit) != 0
}
