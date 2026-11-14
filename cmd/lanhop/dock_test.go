package main

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/rodrigogaraujo/lanhop/internal/edge"
)

func TestTheOrientationOfTheMacDockGivesItsEdge(t *testing.T) {
	for orientation, want := range map[string]edge.Side{
		"":         edge.Bottom,
		"bottom":   edge.Bottom,
		"left":     edge.Left,
		"right":    edge.Right,
		"sideways": 0,
	} {
		if got := dockOfTheMacDock(orientation); got != want {
			t.Errorf("dockOfTheMacDock(%q) = %v, want %v", orientation, got, want)
		}
	}
}

func TestThePositionOfTheUbuntuDockGivesItsEdge(t *testing.T) {
	for position, want := range map[string]edge.Side{
		"'LEFT'\n":   edge.Left,
		"'RIGHT'\n":  edge.Right,
		"'TOP'\n":    edge.Top,
		"'BOTTOM'\n": edge.Bottom,
		"'CENTER'\n": 0,
		"":           0,
	} {
		if got := dockOfThePosition(position); got != want {
			t.Errorf("dockOfThePosition(%q) = %v, want %v", position, got, want)
		}
	}
}

func TestTheNamesOfAGSettingsListAreReadInOrder(t *testing.T) {
	for list, want := range map[string][]string{
		"['ubuntu-dock@ubuntu.com', 'ding@rastersoft.com']\n": {"ubuntu-dock@ubuntu.com", "ding@rastersoft.com"},
		"@as []\n": nil,
		"['broken": nil,
	} {
		if got := namesInTheList(list); !slices.Equal(got, want) {
			t.Errorf("namesInTheList(%q) = %q, want %q", list, got, want)
		}
	}
}

type fakeGnome struct {
	values    map[string]string
	installed []string
	asked     []string
}

func (f *fakeGnome) settings(args ...string) (string, error) {
	key := strings.Join(args, " ")
	f.asked = append(f.asked, key)
	value, known := f.values[key]
	if !known {
		return "", errors.New("No such schema")
	}
	return value, nil
}

func (f *fakeGnome) desk() gnomeDesk {
	return gnomeDesk{
		settings:       f.settings,
		installed:      func(path string) bool { return slices.Contains(f.installed, path) },
		userExtensions: "/home/me/.local/share/gnome-shell/extensions",
	}
}

const (
	enabledKey      = "get org.gnome.shell enabled-extensions"
	disabledKey     = "get org.gnome.shell disabled-extensions"
	userOffKey      = "get org.gnome.shell disable-user-extensions"
	positionKey     = "get org.gnome.shell.extensions.dash-to-dock dock-position"
	userDockFolder  = "/home/me/.local/share/gnome-shell/extensions/dash-to-dock@micxgx.gmail.com"
	userPositionKey = "--schemadir " + userDockFolder + "/schemas " + positionKey
)

func TestTheDockOfUbuntuIsReadOnlyWhileADockExtensionIsOn(t *testing.T) {
	cases := []struct {
		name      string
		values    map[string]string
		installed []string
		want      edge.Side
	}{
		{
			name:      "the dock of Ubuntu on the left",
			values:    map[string]string{enabledKey: "@as []", disabledKey: "@as []", userOffKey: "false", positionKey: "'LEFT'"},
			installed: []string{systemExtensions + "/" + ubuntuDock},
			want:      edge.Left,
		},
		{
			name:      "the dock of Ubuntu switched off",
			values:    map[string]string{enabledKey: "@as []", disabledKey: "['ubuntu-dock@ubuntu.com']", userOffKey: "false", positionKey: "'LEFT'"},
			installed: []string{systemExtensions + "/" + ubuntuDock},
			want:      0,
		},
		{
			name:   "no dock extension installed",
			values: map[string]string{enabledKey: "@as []", disabledKey: "@as []", userOffKey: "false", positionKey: "'BOTTOM'"},
			want:   0,
		},
		{
			name:      "Dash to Dock of the person at the bottom",
			values:    map[string]string{enabledKey: "['dash-to-dock@micxgx.gmail.com']", disabledKey: "@as []", userOffKey: "false", userPositionKey: "'BOTTOM'"},
			installed: []string{userDockFolder, userDockFolder + "/schemas"},
			want:      edge.Bottom,
		},
		{
			name:      "Dash to Dock with the extensions of the person switched off",
			values:    map[string]string{enabledKey: "['dash-to-dock@micxgx.gmail.com']", disabledKey: "@as []", userOffKey: "true", userPositionKey: "'BOTTOM'"},
			installed: []string{userDockFolder, userDockFolder + "/schemas"},
			want:      0,
		},
		{
			name:      "Dash to Dock installed but not enabled",
			values:    map[string]string{enabledKey: "@as []", disabledKey: "@as []", userOffKey: "false", userPositionKey: "'BOTTOM'"},
			installed: []string{userDockFolder, userDockFolder + "/schemas"},
			want:      0,
		},
		{
			name:      "gsettings that cannot read the shell",
			values:    map[string]string{positionKey: "'LEFT'"},
			installed: []string{systemExtensions + "/" + ubuntuDock},
			want:      0,
		},
		{
			name:      "no position to read",
			values:    map[string]string{enabledKey: "@as []", disabledKey: "@as []", userOffKey: "false"},
			installed: []string{systemExtensions + "/" + ubuntuDock},
			want:      0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gnome := &fakeGnome{values: c.values, installed: c.installed}
			if got := gnome.desk().dock(); got != c.want {
				t.Errorf("dock() = %v, want %v, after asking %q", got, c.want, gnome.asked)
			}
		})
	}
}
