//go:build !darwin || !cgo

package update

func checkApp(string, string) error {
	return ErrNoAppCheck
}
