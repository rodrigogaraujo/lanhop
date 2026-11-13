//go:build !darwin || !cgo

package update

func swapInPlace(string, string) error {
	return ErrSwapUnknown
}
