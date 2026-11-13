package main

import (
	"fmt"
	"net"
	"regexp"
	"slices"
	"strings"

	"github.com/rodrigogaraujo/lanhop/internal/pairing"
)

const (
	repository         = "rodrigogaraujo/lanhop"
	installerAddress   = "https://raw.githubusercontent.com/" + repository + "/%s/install.sh"
	addressForTheRoute = "192.0.2.1:9"
)

var releaseVersion = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

var notForJoining = []string{"utun", "tun", "tap", "ppp", "ipsec", "wg", "bridge", "virbr", "docker", "br-", "veth", "vmnet", "vboxnet"}

type networkInterface struct {
	name      string
	up        bool
	loopback  bool
	addresses []net.IP
}

func joinAddresses(interfaces []networkInterface, preferred net.IP) []string {
	var found []string
	for _, candidate := range interfaces {
		if !candidate.up || candidate.loopback || madeForSomethingElse(candidate.name) {
			continue
		}
		for _, address := range candidate.addresses {
			if address.To4() != nil && address.IsPrivate() {
				found = append(found, address.String())
			}
		}
	}
	if preferred == nil {
		return found
	}
	slices.SortStableFunc(found, func(a, b string) int {
		switch {
		case a == preferred.String():
			return -1
		case b == preferred.String():
			return 1
		}
		return 0
	})
	return found
}

func madeForSomethingElse(name string) bool {
	for _, prefix := range notForJoining {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func interfacesOfThisMachine() []networkInterface {
	found, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var interfaces []networkInterface
	for _, each := range found {
		described := networkInterface{
			name:     each.Name,
			up:       each.Flags&net.FlagUp != 0,
			loopback: each.Flags&net.FlagLoopback != 0,
		}
		addresses, _ := each.Addrs()
		for _, address := range addresses {
			if network, ok := address.(*net.IPNet); ok {
				described.addresses = append(described.addresses, network.IP)
			}
		}
		interfaces = append(interfaces, described)
	}
	return interfaces
}

func addressOfTheDefaultRoute() net.IP {
	probe, err := net.Dial("udp", addressForTheRoute)
	if err != nil {
		return nil
	}
	defer probe.Close()
	if local, ok := probe.LocalAddr().(*net.UDPAddr); ok {
		return local.IP
	}
	return nil
}

func joinAddress(host, port string) string {
	if port == fmt.Sprint(pairing.DefaultPort) {
		return host
	}
	return net.JoinHostPort(host, port)
}

func installerLine(running string, extra string) string {
	if releaseVersion.MatchString(running) {
		return fmt.Sprintf("curl -fsSL "+installerAddress+" | LANHOP_VERSION=%s %s sh", running, running, extra)
	}
	return fmt.Sprintf("curl -fsSL "+installerAddress+" | %s sh", "main", extra)
}

func joinLines(running, address string, code pairing.JoinCode) (installer, short string) {
	installer = installerLine(running, fmt.Sprintf("LANHOP_JOIN=%q", address+" "+code.String()))
	short = fmt.Sprintf("lanhop join %s %s", address, code)
	return installer, short
}
