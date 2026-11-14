//go:build !(darwin && cgo)

package macos

import (
	"context"
	"errors"
	"testing"
)

func TestADropNeedsLanhopBuiltWithCgoForMacOS(t *testing.T) {
	drop, err := StartDrop(context.Background(), []PromisedFile{{Name: "report.pdf"}}, func(context.Context, int, string) error { return nil })
	if drop != nil || !errors.Is(err, ErrDropNotBuilt) {
		t.Errorf("StartDrop = %v, %v, want no drop and ErrDropNotBuilt", drop, err)
	}
}

func TestAShieldNeedsLanhopBuiltWithCgoForMacOS(t *testing.T) {
	shield, err := ShieldCursor(context.Background())
	if shield != nil || !errors.Is(err, ErrDropNotBuilt) {
		t.Errorf("ShieldCursor = %v, %v, want no shield and ErrDropNotBuilt", shield, err)
	}
}
