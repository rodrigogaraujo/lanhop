package arrival

import (
	"fmt"
	"time"
)

func quarantineValue(at time.Time) string {
	return fmt.Sprintf("0081;%08x;lanhop;", at.Unix())
}
