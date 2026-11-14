package main

import (
	"strings"

	"github.com/rodrigogaraujo/lanhop/internal/edge"
)

const (
	ubuntuDock         = "ubuntu-dock@ubuntu.com"
	dashToDock         = "dash-to-dock@micxgx.gmail.com"
	dashToDockSchema   = "org.gnome.shell.extensions.dash-to-dock"
	gnomeShellSchema   = "org.gnome.shell"
	systemExtensions   = "/usr/share/gnome-shell/extensions"
	userExtensionsPath = ".local/share/gnome-shell/extensions"
)

func dockOfTheMacDock(orientation string) edge.Side {
	switch strings.TrimSpace(orientation) {
	case "", "bottom":
		return edge.Bottom
	case "left":
		return edge.Left
	case "right":
		return edge.Right
	}
	return 0
}

func dockOfThePosition(position string) edge.Side {
	switch strings.Trim(strings.TrimSpace(position), "'\"") {
	case "LEFT":
		return edge.Left
	case "RIGHT":
		return edge.Right
	case "TOP":
		return edge.Top
	case "BOTTOM":
		return edge.Bottom
	}
	return 0
}

func namesInTheList(list string) []string {
	var names []string
	for rest := list; ; {
		start := strings.IndexByte(rest, '\'')
		if start < 0 {
			return names
		}
		rest = rest[start+1:]
		end := strings.IndexByte(rest, '\'')
		if end < 0 {
			return names
		}
		names = append(names, rest[:end])
		rest = rest[end+1:]
	}
}

type gnomeSettings func(args ...string) (string, error)

type gnomeDesk struct {
	settings       gnomeSettings
	installed      func(path string) bool
	userExtensions string
}

func (g gnomeDesk) dock() edge.Side {
	enabled, err := g.settings("get", gnomeShellSchema, "enabled-extensions")
	if err != nil {
		return 0
	}
	disabled, err := g.settings("get", gnomeShellSchema, "disabled-extensions")
	if err != nil {
		return 0
	}
	userOff, _ := g.settings("get", gnomeShellSchema, "disable-user-extensions")
	isEnabled := func(name string) bool { return listed(enabled, name) && !listed(disabled, name) }
	ubuntuDockOn := g.installed(systemExtensions+"/"+ubuntuDock) && !listed(disabled, ubuntuDock)
	userDock := g.userExtensions + "/" + dashToDock
	dashToDockOn := isEnabled(dashToDock) && strings.TrimSpace(userOff) != "true" && (g.installed(systemExtensions+"/"+dashToDock) || g.installed(userDock))
	if !ubuntuDockOn && !dashToDockOn {
		return 0
	}
	position, err := g.settings("get", dashToDockSchema, "dock-position")
	if err != nil && g.installed(userDock+"/schemas") {
		position, err = g.settings("--schemadir", userDock+"/schemas", "get", dashToDockSchema, "dock-position")
	}
	if err != nil {
		return 0
	}
	return dockOfThePosition(position)
}

func listed(list, name string) bool {
	for _, each := range namesInTheList(list) {
		if each == name {
			return true
		}
	}
	return false
}
