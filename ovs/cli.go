package ovs

import "github.com/codegangsta/cli"

// Exported variables
var (
	FlagIpVlanMode   = cli.StringFlag{Name: "mode", Value: ovsDriverMode, Usage: "name of the OVS driver mode [nat|flat]. (default: l2)"}
	FlagBridgeSubnet = cli.StringFlag{Name: "bridge-subnet", Value: bridgeSubnet, Usage: "(required for flat L2 mode) subnet for the containers on the bridge to use. default only applies to NAT mode: --bridge-subnet=172.18.40.0/24"}
	FlagMtu          = cli.IntFlag{Name: "mtu", Value: defaultMTU, Usage: "MTU of the container interface (default: 1440 Note: greater then 1500 unsupported atm)"}
	FlagGateway      = cli.StringFlag{Name: "gateway", Value: gatewayIP, Usage: "(required for flat L2 mode) IP of the default gateway (default NAT mode: 172.18.40.1)"}
	// Bridge name currently needs to match the docker -run bridge name. Leaving this unmodifiable until that is sorted
	FlagBridgeName = cli.StringFlag{Name: "bridge-name", Value: bridgeName, Usage: "name of the OVS bridge to add containers. (default name: ovsbr-docker0"}
)

// Unexported variables
var (
	bridgeName    = "ovsbr-docker0"  // TODO: currently immutable
	bridgeSubnet  = "172.18.40.0/24" // NAT mode can use this addr. Flat (L2) mode requires an IPNet that will overwrite this val.
	gatewayIP     = ""               // NAT mode will use the first usable address of the bridgeSubnet."172.18.40.0/24" would use "172.18.40.1" as a gateway. Flat L2 mode requires an external gateway for L3 routing
	ovsDriverMode = "nat"            // Default mode is NAT.
	defaultMTU    = 1450
)
