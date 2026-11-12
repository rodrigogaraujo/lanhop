package edge

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"testing"
	"time"
)

const (
	buttonLeft  uint16 = 0x110
	buttonRight uint16 = 0x111
)

var (
	desk      = Bounds{MinX: 0, MinY: 0, MaxX: 2000, MaxY: 1000}
	pushRight = motion{x: 2000, y: 500, dx: 5}
	none      []crossed
)

type motion struct {
	x, y, dx, dy int
}

type step struct {
	atMs  int
	apply func(w *Watcher, now time.Time) (Crossing, bool)
}

type crossed struct {
	atMs       int
	side       Side
	proportion float64
}

func move(atMs int, m motion) []step {
	return []step{{atMs, func(w *Watcher, now time.Time) (Crossing, bool) {
		return w.Move(now, m.x, m.y, m.dx, m.dy)
	}}}
}

func repeatEvery(m motion, everyMs, fromMs, untilMs int) []step {
	var steps []step
	for atMs := fromMs; atMs <= untilMs; atMs += everyMs {
		steps = append(steps, move(atMs, m)...)
	}
	return steps
}

func repeat(m motion, fromMs, untilMs int) []step {
	return repeatEvery(m, 10, fromMs, untilMs)
}

func drift(m motion, stepX, stepY, fromMs, untilMs int) []step {
	var steps []step
	for atMs := fromMs; atMs <= untilMs; atMs += 10 {
		steps = append(steps, move(atMs, m)...)
		m.x += stepX
		m.y += stepY
	}
	return steps
}

func button(atMs int, code uint16, pressed bool) []step {
	return []step{{atMs, func(w *Watcher, _ time.Time) (Crossing, bool) {
		w.Button(code, pressed)
		return Crossing{}, false
	}}}
}

func press(atMs int, code uint16) []step   { return button(atMs, code, true) }
func release(atMs int, code uint16) []step { return button(atMs, code, false) }

func resize(atMs int, bounds Bounds) []step {
	return []step{{atMs, func(w *Watcher, _ time.Time) (Crossing, bool) {
		w.SetBounds(bounds)
		return Crossing{}, false
	}}}
}

func script(parts ...[]step) []step {
	steps := slices.Concat(parts...)
	slices.SortStableFunc(steps, func(a, b step) int { return cmp.Compare(a.atMs, b.atMs) })
	return steps
}

func play(w *Watcher, steps []step) []crossed {
	start := time.Date(2026, time.November, 12, 9, 0, 0, 0, time.UTC)
	var crossings []crossed
	for _, s := range steps {
		now := start.Add(time.Duration(s.atMs) * time.Millisecond)
		if crossing, ok := s.apply(w, now); ok {
			crossings = append(crossings, crossed{s.atMs, crossing.Side, crossing.Proportion})
		}
	}
	return crossings
}

func playOnDesk(settings Settings, steps []step) []crossed {
	w := New(settings)
	w.SetBounds(desk)
	return play(w, steps)
}

func sameCrossings(got, want []crossed) bool {
	return slices.EqualFunc(got, want, func(a, b crossed) bool {
		return a.atMs == b.atMs && a.side == b.side && math.Abs(a.proportion-b.proportion) < 1e-9
	})
}

func expectCrossings(t *testing.T, got, want []crossed) {
	t.Helper()
	if !sameCrossings(got, want) {
		t.Errorf("crossings = %+v, want %+v", got, want)
	}
}

type deskEdge struct {
	side   Side
	length int
	push   func(along int) motion
}

var deskEdges = []deskEdge{
	{Left, 1000, func(along int) motion { return motion{x: 0, y: along, dx: -5} }},
	{Right, 1000, func(along int) motion { return motion{x: 2000, y: along, dx: 5} }},
	{Top, 2000, func(along int) motion { return motion{x: along, y: 0, dy: -5} }},
	{Bottom, 2000, func(along int) motion { return motion{x: along, y: 1000, dy: 5} }},
}

func TestDefaultsArePushFor150msWithA50msGapA700msPauseAndA32UnitCorner(t *testing.T) {
	want := Settings{PushTime: 150 * time.Millisecond, PushGap: 50 * time.Millisecond, PushPause: 700 * time.Millisecond, CornerSize: 32}
	if got := DefaultSettings(); got != want {
		t.Errorf("DefaultSettings = %+v, want %+v", got, want)
	}
}

func TestPushingAnEdgeForThePushTimeCrossesThroughThatSideAtTheMatchingProportion(t *testing.T) {
	for _, e := range deskEdges {
		points := []struct {
			along      int
			proportion float64
		}{
			{32, 32 / float64(e.length)},
			{e.length / 4, 0.25},
			{e.length / 2, 0.5},
			{3 * e.length / 4, 0.75},
			{e.length - 32, 1 - 32/float64(e.length)},
		}
		for _, p := range points {
			t.Run(fmt.Sprintf("%v edge at %d of %d", e.side, p.along, e.length), func(t *testing.T) {
				got := playOnDesk(DefaultSettings(), repeat(e.push(p.along), 0, 300))
				expectCrossings(t, got, []crossed{{150, e.side, p.proportion}})
			})
		}
	}
}

func TestPushingInACornerNeverCrosses(t *testing.T) {
	for _, e := range deskEdges {
		for _, along := range []int{0, 31, e.length - 31, e.length} {
			t.Run(fmt.Sprintf("%v edge at %d of %d", e.side, along, e.length), func(t *testing.T) {
				got := playOnDesk(DefaultSettings(), repeat(e.push(along), 0, 300))
				expectCrossings(t, got, none)
			})
		}
	}
}

func TestACursorReportedPastAnEdgeIsAtThatEdge(t *testing.T) {
	cases := []struct {
		side Side
		push motion
	}{
		{Left, motion{x: -10, y: 500, dx: -5}},
		{Right, motion{x: 2010, y: 500, dx: 5}},
		{Top, motion{x: 1000, y: -10, dy: -5}},
		{Bottom, motion{x: 1000, y: 1010, dy: 5}},
	}
	for _, c := range cases {
		t.Run(c.side.String(), func(t *testing.T) {
			got := playOnDesk(DefaultSettings(), repeat(c.push, 0, 150))
			expectCrossings(t, got, []crossed{{150, c.side, 0.5}})
		})
	}
}

func TestSequencesWithTheDefaultSettings(t *testing.T) {
	cases := []struct {
		name  string
		steps []step
		want  []crossed
	}{
		{
			name: "a touch that comes back at once does not cross",
			steps: script(
				move(0, pushRight),
				move(10, pushRight),
				move(20, motion{x: 1990, y: 500, dx: -10}),
			),
			want: none,
		},
		{
			name:  "a push that stops just before the push time does not cross",
			steps: repeat(pushRight, 0, 140),
			want:  none,
		},
		{
			name:  "a cursor resting at the edge without moving is not pushing",
			steps: repeat(motion{x: 2000, y: 500}, 0, 300),
			want:  none,
		},
		{
			name:  "a cursor sliding along the edge is not pushing",
			steps: drift(motion{x: 2000, y: 300, dy: 5}, 0, 5, 0, 300),
			want:  none,
		},
		{
			name:  "movement away from the edge the cursor rests on is not pushing",
			steps: repeat(motion{x: 2000, y: 500, dx: -5}, 0, 300),
			want:  none,
		},
		{
			name:  "movement towards an edge the cursor has not reached is not pushing",
			steps: repeat(motion{x: 1999, y: 500, dx: 5}, 0, 300),
			want:  none,
		},
		{
			name: "a button held during the whole push stops the crossing",
			steps: script(
				press(0, buttonLeft),
				repeat(pushRight, 0, 300),
			),
			want: none,
		},
		{
			name: "a button pressed in the middle of a push and kept held stops the crossing",
			steps: script(
				repeat(pushRight, 0, 100),
				press(105, buttonLeft),
				repeat(pushRight, 110, 400),
			),
			want: none,
		},
		{
			name: "one of two buttons still held stops the crossing",
			steps: script(
				press(0, buttonLeft),
				press(0, buttonRight),
				release(5, buttonLeft),
				repeat(pushRight, 10, 300),
			),
			want: none,
		},
		{
			name: "a button pressed and released in the middle of a push starts the count over",
			steps: script(
				repeat(pushRight, 0, 100),
				press(102, buttonLeft),
				release(104, buttonLeft),
				repeat(pushRight, 110, 260),
			),
			want: []crossed{{260, Right, 0.5}},
		},
		{
			name: "pushing with a button held does not count towards the push after its release",
			steps: script(
				press(0, buttonLeft),
				repeat(pushRight, 0, 100),
				release(105, buttonLeft),
				repeat(pushRight, 110, 260),
			),
			want: []crossed{{260, Right, 0.5}},
		},
		{
			name: "leaving the edge starts the count over",
			steps: script(
				repeat(pushRight, 0, 100),
				move(110, motion{x: 1995, y: 500, dx: -5}),
				repeat(pushRight, 120, 270),
			),
			want: []crossed{{270, Right, 0.5}},
		},
		{
			name: "moving away while still at the edge starts the count over",
			steps: script(
				repeat(pushRight, 0, 100),
				move(110, motion{x: 2000, y: 500, dx: -1}),
				repeat(pushRight, 120, 270),
			),
			want: []crossed{{270, Right, 0.5}},
		},
		{
			name: "a short pause does not count as pushing, and what was pushed before it is kept",
			steps: script(
				repeat(pushRight, 0, 100),
				repeat(pushRight, 160, 310),
			),
			want: []crossed{{210, Right, 0.5}},
		},
		{
			name: "two short pushes with the finger lifted in between add up",
			steps: script(
				repeat(pushRight, 0, 80),
				repeat(pushRight, 500, 600),
			),
			want: []crossed{{570, Right, 0.5}},
		},
		{
			name: "a pause as long as the longest allowed keeps what was pushed",
			steps: script(
				repeat(pushRight, 0, 100),
				repeat(pushRight, 800, 900),
			),
			want: []crossed{{850, Right, 0.5}},
		},
		{
			name: "a pause longer than the longest allowed starts the count over",
			steps: script(
				repeat(pushRight, 0, 100),
				repeat(pushRight, 810, 1000),
			),
			want: []crossed{{960, Right, 0.5}},
		},
		{
			name: "one quick touch never crosses, however long ago it was",
			steps: script(
				repeat(pushRight, 0, 60),
				move(3000, pushRight),
			),
			want: nil,
		},
		{
			name:  "single nudges, each within the longest pause of the one before, never add up",
			steps: repeatEvery(pushRight, 100, 0, 3000),
			want:  none,
		},
		{
			name: "touches of the edge with the cursor leaving it in between never add up",
			steps: script(
				repeat(pushRight, 0, 80),
				move(200, motion{x: 1990, y: 500, dx: -10}),
				repeat(pushRight, 400, 480),
				move(600, motion{x: 1990, y: 500, dx: -10}),
				repeat(pushRight, 800, 880),
			),
			want: none,
		},
		{
			name: "moving away while still at the edge between two short pushes starts the count over",
			steps: script(
				repeat(pushRight, 0, 100),
				move(300, motion{x: 2000, y: 500, dx: -1}),
				repeat(pushRight, 500, 600),
			),
			want: none,
		},
		{
			name: "a click between two short pushes starts the count over",
			steps: script(
				repeat(pushRight, 0, 100),
				press(300, buttonLeft),
				release(350, buttonLeft),
				repeat(pushRight, 500, 600),
			),
			want: none,
		},
		{
			name: "a push that passes through a corner between two short pushes starts the count over",
			steps: script(
				repeat(pushRight, 0, 100),
				move(300, motion{x: 2000, y: 990, dx: 5}),
				repeat(pushRight, 500, 600),
			),
			want: none,
		},
		{
			name: "a slide along the edge into a corner between two short pushes starts the count over",
			steps: script(
				repeat(pushRight, 0, 100),
				move(300, motion{x: 2000, y: 990, dy: 5}),
				repeat(pushRight, 500, 600),
			),
			want: none,
		},
		{
			name: "short pushes against two edges do not add up to a crossing through either",
			steps: script(
				repeat(pushRight, 0, 100),
				repeat(motion{x: 1000, y: 1000, dy: 5}, 300, 400),
			),
			want: none,
		},
		{
			name: "a pause as long as the gap keeps the count",
			steps: script(
				repeat(pushRight, 0, 100),
				move(150, pushRight),
			),
			want: []crossed{{150, Right, 0.5}},
		},
		{
			name: "movements along the edge between pushing ones keep the count",
			steps: script(
				repeatEvery(motion{x: 2000, y: 500, dx: 1, dy: 5}, 20, 0, 160),
				repeatEvery(motion{x: 2000, y: 500, dy: 5}, 20, 10, 150),
			),
			want: []crossed{{160, Right, 0.5}},
		},
		{
			name:  "a diagonal push crosses where the cursor is when the push time ends",
			steps: drift(motion{x: 2000, y: 300, dx: 5, dy: 10}, 0, 10, 0, 150),
			want:  []crossed{{150, Right, 0.45}},
		},
		{
			name:  "a diagonal push into a corner does not cross",
			steps: repeat(motion{x: 2000, y: 1000, dx: 5, dy: 5}, 0, 300),
			want:  none,
		},
		{
			name:  "a push that slides into a corner before the push time does not cross",
			steps: drift(motion{x: 2000, y: 60, dx: 5, dy: -4}, 0, -4, 0, 300),
			want:  none,
		},
		{
			name:  "a push that starts in a corner counts from where it leaves the corner",
			steps: drift(motion{x: 2000, y: 0, dx: 5, dy: 4}, 0, 4, 0, 230),
			want:  []crossed{{230, Right, 0.092}},
		},
		{
			name:  "a second crossing needs a full new push",
			steps: repeat(pushRight, 0, 400),
			want:  []crossed{{150, Right, 0.5}, {310, Right, 0.5}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expectCrossings(t, playOnDesk(DefaultSettings(), c.steps), c.want)
		})
	}
}

func TestSequencesWithOtherRectangles(t *testing.T) {
	leftOfTheOrigin := Bounds{MinX: -1000, MinY: -400, MaxX: 0, MaxY: 400}
	aroundTheOrigin := Bounds{MinX: -1000, MinY: -400, MaxX: 1000, MaxY: 400}
	absoluteRange := Bounds{MinX: 0, MinY: 0, MaxX: 65535, MaxY: 65535}

	cases := []struct {
		name  string
		steps []step
		want  []crossed
	}{
		{
			name: "the left edge of a rectangle with a negative origin",
			steps: script(
				resize(0, leftOfTheOrigin),
				repeat(motion{x: -1000, y: 0, dx: -5}, 0, 150),
			),
			want: []crossed{{150, Left, 0.5}},
		},
		{
			name: "the right edge of a rectangle with a negative origin",
			steps: script(
				resize(0, leftOfTheOrigin),
				repeat(motion{x: 0, y: -200, dx: 5}, 0, 150),
			),
			want: []crossed{{150, Right, 0.25}},
		},
		{
			name: "the top edge of a rectangle with a negative origin",
			steps: script(
				resize(0, leftOfTheOrigin),
				repeat(motion{x: -750, y: -400, dy: -5}, 0, 150),
			),
			want: []crossed{{150, Top, 0.25}},
		},
		{
			name: "the bottom edge of a rectangle with a negative origin",
			steps: script(
				resize(0, leftOfTheOrigin),
				repeat(motion{x: -250, y: 400, dy: 5}, 0, 150),
			),
			want: []crossed{{150, Bottom, 0.75}},
		},
		{
			name: "a corner of a rectangle with a negative origin does not cross",
			steps: script(
				resize(0, leftOfTheOrigin),
				repeat(motion{x: -1000, y: -390, dx: -5}, 0, 300),
			),
			want: none,
		},
		{
			name: "zero is not an edge inside a rectangle that spans it",
			steps: script(
				resize(0, aroundTheOrigin),
				repeat(motion{x: 0, y: 0, dx: -5, dy: -5}, 0, 300),
			),
			want: none,
		},
		{
			name: "a range from 0 to 65535 on each axis",
			steps: script(
				resize(0, absoluteRange),
				repeat(motion{x: 65535, y: 13107, dx: 40}, 0, 150),
			),
			want: []crossed{{150, Right, 0.2}},
		},
		{
			name: "after the rectangle grows only its new edge crosses",
			steps: script(
				resize(0, desk),
				repeat(pushRight, 0, 100),
				resize(105, Bounds{MinX: 0, MinY: 0, MaxX: 3000, MaxY: 1000}),
				repeat(pushRight, 110, 400),
				repeat(motion{x: 3000, y: 500, dx: 5}, 410, 560),
			),
			want: []crossed{{560, Right, 0.5}},
		},
		{
			name: "a rectangle that shrinks under a push starts the count over",
			steps: script(
				resize(0, desk),
				repeat(pushRight, 0, 100),
				resize(105, Bounds{MinX: 0, MinY: 0, MaxX: 1000, MaxY: 500}),
				repeat(motion{x: 1000, y: 125, dx: 5}, 110, 260),
			),
			want: []crossed{{260, Right, 0.25}},
		},
		{
			name: "the same rectangle told again keeps the count",
			steps: script(
				resize(0, desk),
				repeat(pushRight, 0, 100),
				resize(105, desk),
				repeat(pushRight, 110, 150),
			),
			want: []crossed{{150, Right, 0.5}},
		},
		{
			name: "a rectangle with no width never crosses",
			steps: script(
				resize(0, Bounds{MinX: 5, MinY: 0, MaxX: 5, MaxY: 1000}),
				repeat(motion{x: 5, y: 500, dx: 5}, 0, 300),
			),
			want: none,
		},
		{
			name: "a rectangle with no height never crosses",
			steps: script(
				resize(0, Bounds{MinX: 0, MinY: 7, MaxX: 2000, MaxY: 7}),
				repeat(motion{x: 1000, y: 7, dy: 5}, 0, 300),
			),
			want: none,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expectCrossings(t, play(New(DefaultSettings()), c.steps), c.want)
		})
	}
}

func TestSequencesWithCustomSettings(t *testing.T) {
	const ms = time.Millisecond
	instant := Settings{PushTime: 0, PushGap: 50 * ms, CornerSize: 32}
	wideCorner := Settings{PushTime: 150 * ms, PushGap: 50 * ms, CornerSize: 200}
	noCorner := Settings{PushTime: 150 * ms, PushGap: 50 * ms, CornerSize: 0}

	cases := []struct {
		name     string
		settings Settings
		steps    []step
		want     []crossed
	}{
		{
			name:     "a longer push time delays the crossing",
			settings: Settings{PushTime: 500 * ms, PushGap: 50 * ms, CornerSize: 32},
			steps:    repeat(pushRight, 0, 500),
			want:     []crossed{{500, Right, 0.5}},
		},
		{
			name:     "a shorter push time crosses sooner",
			settings: Settings{PushTime: 30 * ms, PushGap: 50 * ms, CornerSize: 32},
			steps:    repeat(pushRight, 0, 30),
			want:     []crossed{{30, Right, 0.5}},
		},
		{
			name:     "a push time of zero crosses on the first pushing movement",
			settings: instant,
			steps:    move(0, pushRight),
			want:     []crossed{{0, Right, 0.5}},
		},
		{
			name:     "a push time of zero does not cross on a movement along the edge",
			settings: instant,
			steps:    move(0, motion{x: 2000, y: 500, dy: 5}),
			want:     none,
		},
		{
			name:     "a push time of zero does not cross with a button held",
			settings: instant,
			steps:    script(press(0, buttonLeft), move(0, pushRight)),
			want:     none,
		},
		{
			name:     "a push time of zero does not cross in a corner",
			settings: instant,
			steps:    move(0, motion{x: 2000, y: 10, dx: 5}),
			want:     none,
		},
		{
			name:     "a larger corner blocks more of the edge",
			settings: wideCorner,
			steps:    repeat(motion{x: 2000, y: 199, dx: 5}, 0, 300),
			want:     none,
		},
		{
			name:     "a larger corner ends where its size says",
			settings: wideCorner,
			steps:    repeat(motion{x: 2000, y: 200, dx: 5}, 0, 150),
			want:     []crossed{{150, Right, 0.2}},
		},
		{
			name:     "a corner size of zero crosses at the start of the edge",
			settings: noCorner,
			steps:    repeat(motion{x: 2000, y: 0, dx: 5}, 0, 150),
			want:     []crossed{{150, Right, 0}},
		},
		{
			name:     "a corner size of zero crosses at the end of the edge",
			settings: noCorner,
			steps:    repeat(motion{x: 2000, y: 1000, dx: 5}, 0, 150),
			want:     []crossed{{150, Right, 1}},
		},
		{
			name:     "a position beyond the end of the edge counts as its end",
			settings: noCorner,
			steps:    repeat(motion{x: 2000, y: -10, dx: 5}, 0, 150),
			want:     []crossed{{150, Right, 0}},
		},
		{
			name:     "with a corner size of zero the edge pushed first wins in the corner",
			settings: noCorner,
			steps: script(
				repeat(motion{x: 2000, y: 1000, dy: 5}, 0, 90),
				repeat(motion{x: 2000, y: 1000, dx: 5, dy: 5}, 100, 150),
			),
			want: []crossed{{150, Bottom, 1}},
		},
		{
			name:     "with a corner size of zero one push into both edges of the corner crosses once, through the right edge",
			settings: noCorner,
			steps:    repeat(motion{x: 2000, y: 1000, dx: 5, dy: 5}, 0, 300),
			want:     []crossed{{150, Right, 1}},
		},
		{
			name:     "a longer gap keeps the count through a longer pause",
			settings: Settings{PushTime: 150 * ms, PushGap: 200 * ms, CornerSize: 32},
			steps: script(
				repeat(pushRight, 0, 100),
				move(250, pushRight),
			),
			want: []crossed{{250, Right, 0.5}},
		},
		{
			name:     "with no pause set, a pause longer than the gap starts the count over",
			settings: Settings{PushTime: 150 * ms, PushGap: 50 * ms, CornerSize: 32},
			steps: script(
				repeat(pushRight, 0, 100),
				repeat(pushRight, 160, 310),
			),
			want: []crossed{{310, Right, 0.5}},
		},
		{
			name:     "a pause set shorter than the gap changes nothing",
			settings: Settings{PushTime: 150 * ms, PushGap: 50 * ms, PushPause: 20 * ms, CornerSize: 32},
			steps: script(
				repeat(pushRight, 0, 100),
				move(150, pushRight),
			),
			want: []crossed{{150, Right, 0.5}},
		},
		{
			name:     "a longer pause keeps what was pushed through a longer rest",
			settings: Settings{PushTime: 150 * ms, PushGap: 50 * ms, PushPause: 2000 * ms, CornerSize: 32},
			steps: script(
				repeat(pushRight, 0, 100),
				repeat(pushRight, 2100, 2200),
			),
			want: []crossed{{2150, Right, 0.5}},
		},
		{
			name:     "a shorter pause starts the count over after a shorter rest",
			settings: Settings{PushTime: 150 * ms, PushGap: 50 * ms, PushPause: 200 * ms, CornerSize: 32},
			steps: script(
				repeat(pushRight, 0, 100),
				repeat(pushRight, 310, 500),
			),
			want: []crossed{{460, Right, 0.5}},
		},
		{
			name:     "a shorter gap starts the count over between sparse movements",
			settings: Settings{PushTime: 150 * ms, PushGap: 5 * ms, CornerSize: 32},
			steps:    repeat(pushRight, 0, 300),
			want:     none,
		},
		{
			name:     "a shorter gap still crosses with dense movements",
			settings: Settings{PushTime: 150 * ms, PushGap: 5 * ms, CornerSize: 32},
			steps:    repeatEvery(pushRight, 5, 0, 150),
			want:     []crossed{{150, Right, 0.5}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expectCrossings(t, playOnDesk(c.settings, c.steps), c.want)
		})
	}
}

func TestAWatcherThatWasNeverToldARectangleDoesNotCross(t *testing.T) {
	w := New(Settings{})
	got := play(w, repeat(motion{x: 0, y: 0, dx: -5}, 0, 300))
	expectCrossings(t, got, none)
}

func TestSidesHaveNames(t *testing.T) {
	names := map[Side]string{
		Left:    "left",
		Right:   "right",
		Top:     "top",
		Bottom:  "bottom",
		Side(0): "unknown",
	}
	for side, want := range names {
		if got := side.String(); got != want {
			t.Errorf("Side(%d).String() = %q, want %q", int(side), got, want)
		}
	}
}
