package transport

import (
	"sync"
	"time"
)

const (
	Unpaced                   = -1
	defaultDataBytesPerSecond = 10_000_000
	pacedSlice                = 16 << 10
	paceBurst                 = 128 << 10
)

var paceOfThisMachine = NewPace(defaultDataBytesPerSecond)

type Pace struct {
	unpaced        bool
	bytesPerSecond float64
	now            func() time.Time
	sleep          func(time.Duration)

	mutex     sync.Mutex
	allowance float64
	refilled  time.Time
}

func NewPace(bytesPerSecond int) *Pace {
	if bytesPerSecond == 0 {
		bytesPerSecond = defaultDataBytesPerSecond
	}
	return &Pace{
		unpaced:        bytesPerSecond < 0,
		bytesPerSecond: float64(bytesPerSecond),
		now:            time.Now,
		sleep:          time.Sleep,
		allowance:      paceBurst,
	}
}

func (p *Pace) wait(bytes int) {
	if p.unpaced {
		return
	}
	if behind := p.reserve(bytes); behind > 0 {
		p.sleep(behind)
	}
}

func (p *Pace) reserve(bytes int) time.Duration {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	now := p.now()
	if !p.refilled.IsZero() {
		p.allowance = min(paceBurst, p.allowance+now.Sub(p.refilled).Seconds()*p.bytesPerSecond)
	}
	p.refilled = now
	p.allowance -= float64(bytes)
	if p.allowance >= 0 {
		return 0
	}
	return time.Duration(-p.allowance / p.bytesPerSecond * float64(time.Second))
}
