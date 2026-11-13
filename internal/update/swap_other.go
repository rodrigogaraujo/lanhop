//go:build !darwin || !cgo

package update

const SwapsApps = false

func swapInPlace(string, string) error {
	return ErrSwapUnknown
}
