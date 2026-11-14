package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

func ParseInterleaved(set *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := set.Parse(args); err != nil {
			return nil, err
		}
		if set.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, set.Arg(0))
		args = set.Args()[1:]
	}
}

type Printer struct {
	out   io.Writer
	start time.Time
	now   func() time.Time
	lock  sync.Mutex
}

func NewPrinter(out io.Writer) *Printer {
	return &Printer{out: out, start: time.Now(), now: time.Now}
}

func (p *Printer) Say(format string, args ...any) {
	p.lock.Lock()
	defer p.lock.Unlock()
	now := p.now()
	stamp := fmt.Sprintf("%s %+10.1f ms  ", now.Format("15:04:05.000"), float64(now.Sub(p.start).Microseconds())/1000)
	lines := strings.Split(strings.TrimRight(fmt.Sprintf(format, args...), "\n"), "\n")
	fmt.Fprintf(p.out, "%s%s\n", stamp, lines[0])
	indent := strings.Repeat(" ", len(stamp))
	for _, line := range lines[1:] {
		fmt.Fprintf(p.out, "%s%s\n", indent, line)
	}
}
