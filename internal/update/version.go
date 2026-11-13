package update

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
)

const (
	largestPart  = 255
	largestMajor = 32767
)

var (
	ErrNotARelease = errors.New("update: not a release version, which looks like v1.2.3")
	releaseForm    = regexp.MustCompile(`^v(0|[1-9][0-9]{0,4})\.(0|[1-9][0-9]{0,2})\.(0|[1-9][0-9]{0,2})$`)
)

type Version struct {
	Major, Minor, Patch int
}

func ParseVersion(text string) (Version, error) {
	parts := releaseForm.FindStringSubmatch(text)
	if parts == nil {
		return Version{}, fmt.Errorf("%w: %q", ErrNotARelease, text)
	}
	numbers := [3]int{}
	for i := range numbers {
		numbers[i], _ = strconv.Atoi(parts[i+1])
	}
	if numbers[0] > largestMajor || numbers[1] > largestPart || numbers[2] > largestPart {
		return Version{}, fmt.Errorf("%w: %q has a part above %d", ErrNotARelease, text, largestPart)
	}
	return Version{Major: numbers[0], Minor: numbers[1], Patch: numbers[2]}, nil
}

func (v Version) String() string {
	return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
}

func (v Version) Compare(other Version) int {
	switch {
	case v.Major != other.Major:
		return v.Major - other.Major
	case v.Minor != other.Minor:
		return v.Minor - other.Minor
	}
	return v.Patch - other.Patch
}

func (v Version) Value() int32 {
	return int32(v.Major<<16 | v.Minor<<8 | v.Patch)
}
