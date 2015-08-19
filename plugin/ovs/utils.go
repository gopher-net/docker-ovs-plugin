package ovs

import (
	"fmt"
	"net"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/vishvananda/netlink"
)

// Generate a mac addr
func makeMac(ip net.IP) string {
	hw := make(net.HardwareAddr, 6)
	hw[0] = 0x7a
	hw[1] = 0x42
	copy(hw[2:], ip.To4())
	return hw.String()
}

// Return the IPv4 address of a network interface
func getIfaceAddr(name string) (*net.IPNet, error) {
	iface, err := netlink.LinkByName(name)
	if err != nil {
		return nil, err
	}
	addrs, err := netlink.AddrList(iface, netlink.FAMILY_V4)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("Interface %s has no IP addresses", name)
	}
	if len(addrs) > 1 {
		log.Infof("Interface [ %v ] has more than 1 IPv4 address. Defaulting to using [ %v ]\n", name, addrs[0].IP)
	}
	return addrs[0].IPNet, nil
}

// Set the IP addr of a netlink interface
func (driver *driver) setInterfaceIP(name string, rawIP string) error {
	var netlinkRetryTimer time.Duration
	netlinkRetryTimer = 2
	iface, err := netlink.LinkByName(name)
	if err != nil {
		log.Debugf("error retrieving new OVS bridge netlink link [ %s ] allowing another [ %d ] seconds for the host to finish creating it..", bridgeName, netlinkRetryTimer)
		time.Sleep(netlinkRetryTimer * time.Second)
		iface, err = netlink.LinkByName(name)
		if err != nil {
			log.Debugf("error retrieving new OVS bridge netlink link [ %s ] allowing another [ %d ] seconds for the host to finish creating it..", bridgeName, netlinkRetryTimer)
			time.Sleep(netlinkRetryTimer * time.Second)
			iface, err = netlink.LinkByName(name)
			if err != nil {
				log.Fatalf("Abandoning retrieving the new OVS bridge link from netlink, Run [ ip link ] to troubleshoot the error: %s", err)
				return err
			}
		}
	}
	ipNet, err := netlink.ParseIPNet(rawIP)
	if err != nil {
		return err
	}
	addr := &netlink.Addr{ipNet, ""}
	return netlink.AddrAdd(iface, addr)
}

// Increment an IP in a subnet
func ipIncrement(networkAddr net.IP) net.IP {
	for i := 15; i >= 0; i-- {
		b := networkAddr[i]
		if b < 255 {
			networkAddr[i] = b + 1
			for xi := i + 1; xi <= 15; xi++ {
				networkAddr[xi] = 0
			}
			break
		}
	}
	return networkAddr
}
