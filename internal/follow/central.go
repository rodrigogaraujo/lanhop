package follow

import (
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rodrigogaraujo/lanhop/internal/event"
	"github.com/rodrigogaraujo/lanhop/internal/features"
	"github.com/rodrigogaraujo/lanhop/internal/sharer"
	"github.com/rodrigogaraujo/lanhop/internal/update"
)

const DefaultVersionPatience = 5 * time.Second

type Mark string

const (
	Follows           Mark = "follows"
	DoesNotFollow     Mark = "does not follow"
	NeedsTheInstaller Mark = "needs the installer"
	NotKnownYet       Mark = ""
)

type Seat struct {
	Name    string
	Version update.Version
	Mark    Mark
}

type CentralConfig struct {
	Running         update.Version
	From            update.Version
	VersionPatience time.Duration
	OnChange        func(seats []Seat)
}

type sideMachine struct {
	name        string
	follows     bool
	version     update.Version
	sentVersion bool
	release     bool
	updateSent  bool
	silent      bool
	patience    *time.Timer
}

type Central struct {
	config    CentralConfig
	mutex     sync.Mutex
	reporting sync.Mutex
	machines  map[sharer.Conn]*sideMachine
}

func NewCentral(config CentralConfig) *Central {
	if config.VersionPatience == 0 {
		config.VersionPatience = DefaultVersionPatience
	}
	if config.OnChange == nil {
		config.OnChange = func([]Seat) {}
	}
	return &Central{config: config, machines: map[sharer.Conn]*sideMachine{}}
}

func (c *Central) Connected(conn sharer.Conn) func(event.Event) {
	machine := &sideMachine{name: conn.Peer().Name}
	c.mutex.Lock()
	c.machines[conn] = machine
	machine.patience = time.AfterFunc(c.config.VersionPatience, func() { c.noVersionCame(conn, machine) })
	c.mutex.Unlock()
	c.changed()
	return func(frame event.Event) { c.heard(conn, machine, frame) }
}

func (c *Central) Lost(conn sharer.Conn) {
	c.mutex.Lock()
	if machine, found := c.machines[conn]; found {
		machine.patience.Stop()
		delete(c.machines, conn)
	}
	c.mutex.Unlock()
	c.changed()
}

func (c *Central) FocusLeaves(sharer.Conn)  {}
func (c *Central) FocusArrives(sharer.Conn) {}

func (c *Central) heard(conn sharer.Conn, machine *sideMachine, frame event.Event) {
	if !frame.IsControl() {
		return
	}
	c.mutex.Lock()
	if c.machines[conn] != machine {
		c.mutex.Unlock()
		return
	}
	switch frame.Code {
	case event.ControlFeatures:
		follows := frame.Value&(1<<features.Follows) != 0
		if !follows {
			machine.updateSent = false
		}
		machine.follows = follows
	case event.ControlVersion:
		machine.sentVersion = true
		machine.version, machine.release = update.VersionFromValue(frame.Value)
		machine.patience.Stop()
	default:
		c.mutex.Unlock()
		return
	}
	send := c.shouldUpdate(machine)
	if send {
		machine.updateSent = true
	}
	c.mutex.Unlock()
	if send {
		conn.Send(event.Update(c.config.Running.Value()))
	}
	c.changed()
}

func (c *Central) shouldUpdate(machine *sideMachine) bool {
	return c.config.Running != (update.Version{}) &&
		machine.follows && machine.sentVersion && machine.release && !machine.updateSent &&
		machine.version.Compare(c.config.Running) < 0 &&
		machine.version.Compare(c.config.From) >= 0
}

func (c *Central) noVersionCame(conn sharer.Conn, machine *sideMachine) {
	c.mutex.Lock()
	still := c.machines[conn] == machine && !machine.sentVersion
	if still {
		machine.silent = true
	}
	c.mutex.Unlock()
	if still {
		c.changed()
	}
}

func (c *Central) markOf(machine *sideMachine) Mark {
	switch {
	case machine.silent:
		return NeedsTheInstaller
	case !machine.sentVersion:
		return NotKnownYet
	case machine.release && machine.version.Compare(c.config.From) < 0:
		return NeedsTheInstaller
	case machine.follows:
		return Follows
	}
	return DoesNotFollow
}

func (c *Central) Seats() []Seat {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	seats := make([]Seat, 0, len(c.machines))
	for _, machine := range c.machines {
		seats = append(seats, Seat{Name: machine.name, Version: machine.version, Mark: c.markOf(machine)})
	}
	slices.SortFunc(seats, func(a, b Seat) int { return strings.Compare(a.Name, b.Name) })
	return seats
}

func (c *Central) changed() {
	c.reporting.Lock()
	defer c.reporting.Unlock()
	c.config.OnChange(c.Seats())
}
