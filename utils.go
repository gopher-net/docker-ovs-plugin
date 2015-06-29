package plugin

import (
	"errors"
	"net"

	"github.com/docker/libnetwork/netutils"
	"github.com/vishvananda/netlink"
)

// utils

func vethPair(suffix string) *netlink.Veth {
	return &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{Name: "vethwl" + suffix},
		PeerName:  "vethwg" + suffix,
	}
}

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
	if config.AddressIPv4 != nil {
		return config.AddressIPv4, nil
	}

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
