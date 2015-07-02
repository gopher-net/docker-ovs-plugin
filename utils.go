package main

import (
	"errors"
	"net"

	"bytes"
	"github.com/docker/libnetwork/netutils"
	"io/ioutil"
	"regexp"
)

// utils
const (
	ipv4NumBlock = `(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)`
	ipv4Address  = `(` + ipv4NumBlock + `\.){3}` + ipv4NumBlock

	// This is not an IPv6 address verifier as it will accept a super-set of IPv6, and also
	// will *not match* IPv4-Embedded IPv6 Addresses (RFC6052), but that and other variants
	// -- e.g. other link-local types -- either won't work in containers or are unnecessary.
	// For readability and sufficiency for Docker purposes this seemed more reasonable than a
	// 1000+ character regexp with exact and complete IPv6 validation
	ipv6Address = `([0-9A-Fa-f]{0,4}:){2,7}([0-9A-Fa-f]{0,4})`
)

var nsRegexp = regexp.MustCompile(`^\s*nameserver\s*((` + ipv4Address + `)|(` + ipv6Address + `))\s*$`)

func makeMac(ip net.IP) string {
	hw := make(net.HardwareAddr, 6)
	hw[0] = 0x7a
	hw[1] = 0x42
	copy(hw[2:], ip.To4())
	return hw.String()
}

// getNameserversAsCIDR returns nameservers (if any) listed in
// /etc/resolv.conf as CIDR blocks (e.g., "1.2.3.4/32")
// This function's output is intended for net.ParseCIDR
func getNameserversAsCIDR(resolvConf []byte) []string {
	nameservers := []string{}
	for _, nameserver := range getNameservers(resolvConf) {
		nameservers = append(nameservers, nameserver+"/32")
	}
	return nameservers
}

func findBridgeCIDR() (*net.IPNet, error) {

	// Use the requested IPv4 CIDR when available.
	// if config.AddressIPv4 != nil {
	//	return config.AddressIPv4, nil
	// }

	// We don't check for an error here, because we don't really care if we
	// can't read /etc/resolv.conf. So instead we skip the append if resolvConf
	// is nil. It either doesn't exist, or we can't read it for some reason.
	nameservers := []string{}
	if resolvConf, _ := readResolvConf(); resolvConf != nil {
		nameservers = append(nameservers, getNameserversAsCIDR(resolvConf)...)
	}

	// Try to automatically elect appropriate bridge IPv4 settings.
	for _, n := range bridgeNetworks {
		if err := netutils.CheckNameserverOverlaps(nameservers, n); err == nil {
			if err := netutils.CheckRouteOverlaps(n); err == nil {
				return n, nil
			}
		}
	}

	return nil, errors.New("cannot find available address for bridge")

}

// GetNameservers returns nameservers (if any) listed in /etc/resolv.conf
func getNameservers(resolvConf []byte) []string {
	nameservers := []string{}
	for _, line := range getLines(resolvConf, []byte("#")) {
		var ns = nsRegexp.FindSubmatch(line)
		if len(ns) > 0 {
			nameservers = append(nameservers, string(ns[1]))
		}
	}
	return nameservers
}

// getLines parses input into lines and strips away comments.
func getLines(input []byte, commentMarker []byte) [][]byte {
	lines := bytes.Split(input, []byte("\n"))
	var output [][]byte
	for _, currentLine := range lines {
		var commentIndex = bytes.Index(currentLine, commentMarker)
		if commentIndex == -1 {
			output = append(output, currentLine)
		} else {
			output = append(output, currentLine[:commentIndex])
		}
	}
	return output
}

func readResolvConf() ([]byte, error) {
	resolv, err := ioutil.ReadFile("/etc/resolv.conf")
	if err != nil {
		return nil, err
	}
	return resolv, nil
}
