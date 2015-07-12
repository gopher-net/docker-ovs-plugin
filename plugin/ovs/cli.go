package ovs

import "github.com/codegangsta/cli"

// Exported variables
var (
	// TODO: Values need to be bound to driver. Need to modify the Driver iface. Added brOpts if we want to pass that to Listen(string)
	FlagBridgeName   = cli.StringFlag{Name: "bridge-name", Value: bridgeName, Usage: "name of the OVS bridge to add containers. If it doees not exist, it will be created. default: --bridge-name=ovsbr-docker0"}
	FlagBridgeIP     = cli.StringFlag{Name: "bridge-net", Value: bridgeIfaceNet, Usage: "IP and netmask of the bridge. default: --bridge-ip=172.18.40.1/24"}
	FlagBridgeSubnet = cli.StringFlag{Name: "bridge-subnet", Value: bridgeSubnet, Usage: "subnet for the containers on the bridge to use (currently IPv4 support). default: --bridge-subnet=172.18.40.0/24"}
)

// Unexported variables
var (
	// TODO: Temp hardcodes, bind to CLI flags and/or dnet-ctl for bridge properties.
	bridgeName     = "ovsbr-docker0"  // temp until binding via flags
	bridgeSubnet   = "172.18.40.0/24" // temp until binding via flags
	bridgeIfaceNet = "172.18.40.1/24" // temp until binding via flags
	gatewayIP      = "172.18.40.1"    // Bridge vs. GW IPs
)
