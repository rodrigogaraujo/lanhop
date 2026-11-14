//go:build darwin && cgo

package macos

import (
	"slices"
	"testing"
)

func TestTheDockOrientationIsOneThatTheDockOffersOrUnset(t *testing.T) {
	if orientation := DockOrientation(); !slices.Contains([]string{"", "left", "bottom", "right"}, orientation) {
		t.Errorf("DockOrientation() = %q, want left, bottom, right or nothing", orientation)
	}
}
