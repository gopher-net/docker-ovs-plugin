package ovs

import (
	"fmt"
	"strings"
	"time"

	log "github.com/Sirupsen/logrus"
	networkplugin "github.com/docker/go-plugins-helpers/network"
	"github.com/samalba/dockerclient"
	"github.com/socketplane/libovsdb"
	"github.com/vishvananda/netlink"
)

const (
	DriverName       = "ovs"
	defaultRoute     = "0.0.0.0/0"
	ovsPortPrefix    = "ovs-veth0-"
	bridgePrefix     = "ovsbr-"
	containerEthName = "eth"

	mtuOption           = "net.gopher.ovs.bridge.mtu"
	modeOption          = "net.gopher.ovs.bridge.mode"
	bridgeNameOption    = "net.gopher.ovs.bridge.name"
	bindInterfaceOption = "net.gopher.ovs.bridge.bind_interface"

	modeNAT  = "nat"
	modeFlat = "flat"

	defaultMTU  = 1500
	defaultMode = modeNAT
)

var (
	validModes = map[string]bool{
		modeNAT:  true,
		modeFlat: true,
	}
)

type Driver struct {
	dockerer
	ovsdber
	networks map[string]*NetworkState
	OvsdbNotifier
}

// NetworkState is filled in at network creation time
// it contains state that we wish to keep for each network
type NetworkState struct {
	BridgeName        string
	MTU               int
	Mode              string
	Gateway           string
	GatewayMask       string
	FlatBindInterface string
}

func (d *Driver) CreateNetwork(r *networkplugin.CreateNetworkRequest) error {
	log.Debugf("Create network request: %+v", r)

	bridgeName, err := getBridgeName(r)
	if err != nil {
		return err
	}

	mtu, err := getBridgeMTU(r)
	if err != nil {
		return err
	}

	mode, err := getBridgeMode(r)
	if err != nil {
		return err
	}

	gateway, mask, err := getGatewayIP(r)
	if err != nil {
		return err
	}

	bindInterface, err := getBindInterface(r)
	if err != nil {
		return err
	}

	ns := &NetworkState{
		BridgeName:        bridgeName,
		MTU:               mtu,
		Mode:              mode,
		Gateway:           gateway,
		GatewayMask:       mask,
		FlatBindInterface: bindInterface,
	}
	d.networks[r.NetworkID] = ns

	log.Debugf("Initializing bridge for network %s", r.NetworkID)
	if err := d.initBridge(r.NetworkID); err != nil {
		delete(d.networks, r.NetworkID)
		return err
	}
	return nil
}

func (d *Driver) DeleteNetwork(r *networkplugin.DeleteNetworkRequest) error {
	log.Debugf("Delete network request: %+v", r)
	bridgeName := d.networks[r.NetworkID].BridgeName
	log.Debugf("Deleting Bridge %s", bridgeName)
	err := d.deleteBridge(bridgeName)
	if err != nil {
		log.Errorf("Deleting bridge %s failed: %s", bridgeName, err)
		return err
	}
	delete(d.networks, r.NetworkID)
	return nil
}

func (d *Driver) CreateEndpoint(r *networkplugin.CreateEndpointRequest) (*networkplugin.CreateEndpointResponse,error) {
	log.Debugf("Create endpoint request: %+v", r)
	localVethPair := vethPair(truncateID(r.EndpointID))
	log.Debugf("Create vethPair")
    	res := &networkplugin.CreateEndpointResponse{Interface: &networkplugin.EndpointInterface{MacAddress: localVethPair.Attrs().HardwareAddr.String()}}
    	log.Debugf("Attached veth5 %+v," ,r.Interface)
    	return res,nil
}
func (d *Driver) GetCapabilities () (*networkplugin.CapabilitiesResponse,error) {
        log.Debugf("Get capabilities request")
        res := &networkplugin.CapabilitiesResponse{
                Scope:"local",
        }
        return res,nil
}
func (d *Driver) ProgramExternalConnectivity (r *networkplugin.ProgramExternalConnectivityRequest) error {
        log.Debugf("Program External Connectivity  request: %+v", r)
	return nil
}

func (d *Driver) RevokeExternalConnectivity (r *networkplugin.RevokeExternalConnectivityRequest) error {
        log.Debugf("Revoke external connectivity request: %+v", r)
        return nil
}
func (d *Driver) FreeNetwork (r *networkplugin.FreeNetworkRequest) error {
        log.Debugf("Free network request: %+v", r)
        return nil
}
func (d *Driver) DiscoverNew (r *networkplugin.DiscoveryNotification) error {
        log.Debugf("Discover new request: %+v", r)
        return nil
}
func (d *Driver) DiscoverDelete (r *networkplugin.DiscoveryNotification) error {
        log.Debugf("Discover delete request: %+v", r)
        return nil
}
func (d *Driver) DeleteEndpoint(r *networkplugin.DeleteEndpointRequest) error {
	log.Debugf("Delete endpoint request: %+v", r)
	return nil
}

func (d *Driver) AllocateNetwork(r *networkplugin.AllocateNetworkRequest) (*networkplugin.AllocateNetworkResponse,error) {
        log.Debugf("Allocate network request: %+v", r)
        res := &networkplugin.AllocateNetworkResponse{
                Options: make(map[string]string),
        }
        return res,nil
}
func (d *Driver) EndpointInfo(r *networkplugin.InfoRequest) (*networkplugin.InfoResponse, error) {
	res := &networkplugin.InfoResponse{
		Value: make(map[string]string),
	}
	return res, nil
}

func (d *Driver) Join(r *networkplugin.JoinRequest) (*networkplugin.JoinResponse, error) {
	// create and attach local name to the bridge
	log.Debugf("Join request: %+v", r)
	localVethPair := vethPair(truncateID(r.EndpointID))
	if err := netlink.LinkAdd(localVethPair); err != nil {
		log.Errorf("failed to create the veth pair named: [ %v ] error: [ %s ] ", localVethPair, err)
		return nil, err
	}
	// Bring the veth pair up
	err := netlink.LinkSetUp(localVethPair)
	if err != nil {
		log.Warnf("Error enabling  Veth local iface: [ %v ]", localVethPair)
		return nil, err
	}
	bridgeName := d.networks[r.NetworkID].BridgeName
	err = d.addOvsVethPort(bridgeName, localVethPair.Name, 0)
	if err != nil {
		log.Errorf("error attaching veth [ %s ] to bridge [ %s ]", localVethPair.Name, bridgeName)
		return nil, err
	}
	log.Infof("Attached veth [ %s ] to bridge [ %s ]", localVethPair.Name, bridgeName)

	// SrcName gets renamed to DstPrefix + ID on the container iface
	res := &networkplugin.JoinResponse{
		InterfaceName: networkplugin.InterfaceName{
			SrcName:   localVethPair.PeerName,
			DstPrefix: containerEthName,
		},
		Gateway: d.networks[r.NetworkID].Gateway,
	}
	log.Debugf("Join endpoint %s:%s to %s", r.NetworkID, r.EndpointID, r.SandboxKey)
	return res, nil
}

func (d *Driver) Leave(r *networkplugin.LeaveRequest) error {
	log.Debugf("Leave request: %+v", r)
	localVethPair := vethPair(truncateID(r.EndpointID))
	if err := netlink.LinkDel(localVethPair); err != nil {
		log.Errorf("unable to delete veth on leave: %s", err)
	}
	portID := fmt.Sprintf(ovsPortPrefix + truncateID(r.EndpointID))
	bridgeName := d.networks[r.NetworkID].BridgeName
	err := d.ovsdber.deletePort(bridgeName, portID)
	if err != nil {
		log.Errorf("OVS port [ %s ] delete transaction failed on bridge [ %s ] due to: %s", portID, bridgeName, err)
		return err
	}
	log.Infof("Deleted OVS port [ %s ] from bridge [ %s ]", portID, bridgeName)
	log.Debugf("Leave %s:%s", r.NetworkID, r.EndpointID)
	return nil
}

func NewDriver() (*Driver, error) {
	docker, err := dockerclient.NewDockerClient("unix:///var/run/docker.sock", nil)
	if err != nil {
		return nil, fmt.Errorf("could not connect to docker: %s", err)
	}

	// initiate the ovsdb manager port binding
	var ovsdb *libovsdb.OvsdbClient
	retries := 3
	for i := 0; i < retries; i++ {
		//ovsdb, err = libovsdb.Connect(localhost, ovsdbPort)
		ovsdb, err = libovsdb.ConnectWithUnixSocket("/var/run/openvswitch/db.sock")
		if err == nil {
			break
		}
		log.Errorf("could not connect to openvswitch : %s. Retrying in 5 seconds", err)
		time.Sleep(5 * time.Second)
	}

	if ovsdb == nil {
		return nil, fmt.Errorf("could not connect to open vswitch")
	}

	d := &Driver{
		dockerer: dockerer{
			client: docker,
		},
		ovsdber: ovsdber{
			ovsdb: ovsdb,
		},
		networks: make(map[string]*NetworkState),
	}
	//recover networks
	netlist,err :=d.dockerer.client.ListNetworks("")
	if err != nil {
		return nil, fmt.Errorf("could not get  docker networks: %s", err)
	}
	for _, net := range  netlist{
		if net.Driver  == DriverName{
			netInspect,err:=d.dockerer.client.InspectNetwork(net.ID)
			if err != nil {
				return nil, fmt.Errorf("could not inpect docker networks inpect: %s", err)
			}
			bridgeName, err := getBridgeNamefromresource(netInspect)
			if err != nil {
				return nil,err
			}
			ns := &NetworkState{
				BridgeName:        bridgeName,
			}
			d.networks[net.ID] = ns
			log.Debugf("exist network create by this driver:%v",netInspect.Name)
		}
	}
	// Initialize ovsdb cache at rpc connection setup
	d.ovsdber.initDBCache()
	return d, nil
}

// Create veth pair. Peername is renamed to eth0 in the container
func vethPair(suffix string) *netlink.Veth {
	return &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{Name: ovsPortPrefix + suffix},
		PeerName:  "ethc" + suffix,
	}
}

// Enable a netlink interface
func interfaceUp(name string) error {
	iface, err := netlink.LinkByName(name)
	if err != nil {
		log.Debugf("Error retrieving a link named [ %s ]", iface.Attrs().Name)
		return err
	}
	return netlink.LinkSetUp(iface)
}

func truncateID(id string) string {
	return id[:5]
}

func getBridgeMTU(r *networkplugin.CreateNetworkRequest) (int, error) {
	bridgeMTU := defaultMTU
	if r.Options != nil {
		if mtu, ok := r.Options[mtuOption].(int); ok {
			bridgeMTU = mtu
		}
	}
	return bridgeMTU, nil
}

func getBridgeName(r *networkplugin.CreateNetworkRequest) (string, error) {
	bridgeName := bridgePrefix + truncateID(r.NetworkID)
	if r.Options != nil {
		if name, ok := r.Options[bridgeNameOption].(string); ok {
			bridgeName = name
		}
	}
	return bridgeName, nil
}

func getBridgeMode(r *networkplugin.CreateNetworkRequest) (string, error) {
	bridgeMode := defaultMode
	if r.Options != nil {
		if mode, ok := r.Options[modeOption].(string); ok {
			if _, isValid := validModes[mode]; !isValid {
				return "", fmt.Errorf("%s is not a valid mode", mode)
			}
			bridgeMode = mode
		}
	}
	return bridgeMode, nil
}

func getGatewayIP(r *networkplugin.CreateNetworkRequest) (string, string, error) {
	// FIXME: Dear future self, I'm sorry for leaving you with this mess, but I want to get this working ASAP
	// This should be an array
	// We need to handle case where we have
	// a. v6 and v4 - dual stack
	// auxilliary address
	// multiple subnets on one network
	// also in that case, we'll need a function to determine the correct default gateway based on it's IP/Mask
	var gatewayIP string

	if len(r.IPv6Data) > 0 {
		if r.IPv6Data[0] != nil {
			if r.IPv6Data[0].Gateway != "" {
				gatewayIP = r.IPv6Data[0].Gateway
			}
		}
	}
	// Assumption: IPAM will provide either IPv4 OR IPv6 but not both
	// We may want to modify this in future to support dual stack
	if len(r.IPv4Data) > 0 {
		if r.IPv4Data[0] != nil {
			if r.IPv4Data[0].Gateway != "" {
				gatewayIP = r.IPv4Data[0].Gateway
			}
		}
	}

	if gatewayIP == "" {
		return "", "", fmt.Errorf("No gateway IP found")
	}
	parts := strings.Split(gatewayIP, "/")
	if parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("Cannot split gateway IP address")
	}
	return parts[0], parts[1], nil
}

func getBindInterface(r *networkplugin.CreateNetworkRequest) (string, error) {
	if r.Options != nil {
		if mode, ok := r.Options[bindInterfaceOption].(string); ok {
			return mode, nil
		}
	}
	// As bind interface is optional and has no default, don't return an error
	return "", nil
}
func getBridgeNamefromresource(r *dockerclient.NetworkResource) (string, error) {
	bridgeName := bridgePrefix + truncateID(r.ID)
	if r.Options != nil {
		if name, ok := r.Options[bridgeNameOption]; ok {
			bridgeName = name
		}
	}
	return bridgeName, nil
}
