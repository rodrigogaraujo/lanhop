package transport

import (
	"math"
	"slices"
	"sync"
	"time"
)

const (
	Unpaced                   = -1
	defaultDataBytesPerSecond = 10_000_000
	pacedSlice                = 16 << 10
	paceBurst                 = 128 << 10
)

const (
	startingBytesPerSecond  = 40_000_000 / 8
	lowestBytesPerSecond    = 1_000_000 / 8
	risingBytesPerSecond    = 20_000_000 / 8
	queueTarget             = 15 * time.Millisecond
	decreasesAtMostEvery    = 50 * time.Millisecond
	delayStaysFreshFor      = 200 * time.Millisecond
	idleBeforeStartingAgain = time.Second
	longestStep             = 100 * time.Millisecond
	growthEvery100ms        = 2.0
	samplesInTheMinimum     = 4
)

var paceOfThisMachine = newPaceThatFollowsTheDelay(defaultDataBytesPerSecond)

type Pace struct {
	unpaced        bool
	bytesPerSecond float64
	now            func() time.Time
	sleep          func(time.Duration)

	mutex     sync.Mutex
	allowance float64
	refilled  time.Time

	followsTheDelay bool
	current         float64
	growing         bool
	delays          map[any]*delayOfOneConnection
	stepped         time.Time
	decreased       time.Time
	heard           time.Time
}

type delayOfOneConnection struct {
	base   time.Duration
	recent []time.Duration
	heard  time.Time
}

func (d *delayOfOneConnection) queue() time.Duration {
	return slices.Min(d.recent) - d.base
}

func NewPace(bytesPerSecond int) *Pace {
	if bytesPerSecond == 0 {
		return newPaceThatFollowsTheDelay(defaultDataBytesPerSecond)
	}
	return &Pace{
		unpaced:        bytesPerSecond < 0,
		bytesPerSecond: float64(bytesPerSecond),
		now:            time.Now,
		sleep:          time.Sleep,
	}
}

func newPaceThatFollowsTheDelay(ceiling int) *Pace {
	return &Pace{
		bytesPerSecond:  float64(ceiling),
		now:             time.Now,
		sleep:           time.Sleep,
		followsTheDelay: true,
		current:         min(startingBytesPerSecond, float64(ceiling)),
		growing:         true,
		delays:          map[any]*delayOfOneConnection{},
	}
}

func (p *Pace) observe(connection any, roundTrip time.Duration) {
	if p.unpaced || !p.followsTheDelay {
		return
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	now := p.clock()
	if p.heard.IsZero() || now.Sub(p.heard) > idleBeforeStartingAgain {
		p.startAgain(now)
	}
	p.heard = now
	delay, known := p.delays[connection]
	if !known {
		delay = &delayOfOneConnection{base: roundTrip}
		p.delays[connection] = delay
	}
	delay.base = min(delay.base, roundTrip)
	delay.recent = append(delay.recent, roundTrip)
	if len(delay.recent) > samplesInTheMinimum {
		delay.recent = delay.recent[1:]
	}
	delay.heard = now
	p.follow(now, p.worstQueue(now))
}

func (p *Pace) startAgain(now time.Time) {
	p.current = min(startingBytesPerSecond, p.bytesPerSecond)
	p.growing = true
	p.stepped = now
	p.decreased = time.Time{}
	clear(p.delays)
}

func (p *Pace) worstQueue(now time.Time) time.Duration {
	var worst time.Duration
	for connection, delay := range p.delays {
		age := now.Sub(delay.heard)
		if age > idleBeforeStartingAgain {
			delete(p.delays, connection)
			continue
		}
		if age <= delayStaysFreshFor {
			worst = max(worst, delay.queue())
		}
	}
	return worst
}

func (p *Pace) follow(now time.Time, queue time.Duration) {
	step := min(now.Sub(p.stepped), longestStep)
	p.stepped = now
	switch {
	case queue > queueTarget:
		p.growing = false
		if p.decreased.IsZero() || now.Sub(p.decreased) >= decreasesAtMostEvery {
			over := min(1, float64(queue-queueTarget)/float64(queueTarget))
			p.current *= 0.9 - 0.4*over
			p.decreased = now
		}
	case p.growing && queue < queueTarget/3:
		p.current *= math.Pow(growthEvery100ms, step.Seconds()/0.1)
	default:
		p.current += (1 - float64(queue)/float64(queueTarget)) * risingBytesPerSecond * step.Seconds()
	}
	p.current = min(max(p.current, lowestBytesPerSecond), p.bytesPerSecond)
}

func (p *Pace) currentBytesPerSecond() float64 {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.rate()
}

func (p *Pace) wait(bytes int) {
	if p.unpaced {
		return
	}
	if behind := p.reserve(bytes); behind > 0 {
		p.pause(behind)
	}
}

func (p *Pace) reserve(bytes int) time.Duration {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	now := p.clock()
	if p.refilled.IsZero() {
		p.allowance = paceBurst
	} else {
		p.allowance = min(paceBurst, p.allowance+now.Sub(p.refilled).Seconds()*p.rate())
	}
	p.refilled = now
	p.allowance -= float64(bytes)
	if p.allowance >= 0 {
		return 0
	}
	return time.Duration(-p.allowance / p.rate() * float64(time.Second))
}

func (p *Pace) rate() float64 {
	if p.followsTheDelay && p.current > 0 {
		return p.current
	}
	if p.bytesPerSecond > 0 {
		return p.bytesPerSecond
	}
	return defaultDataBytesPerSecond
}

func (p *Pace) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

func (p *Pace) pause(behind time.Duration) {
	if p.sleep != nil {
		p.sleep(behind)
		return
	}
	time.Sleep(behind)
}
