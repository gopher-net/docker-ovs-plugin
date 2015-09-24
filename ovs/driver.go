package ovs

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	log "github.com/Sirupsen/logrus"
	"github.com/docker/libnetwork/ipallocator"
	"github.com/gorilla/mux"
	"github.com/vishvananda/netlink"
)

const (
	MethodReceiver   = "NetworkDriver"
	defaultRoute     = "0.0.0.0/0"
	ovsPortPrefix    = "ovs-veth0-"
	containerEthName = "eth"
)

type Driver interface {
	Listen(string) error
}

// Struct for binding plugin specific configurations (cli.go for details).
type pluginConfig struct {
	mtu        int
	bridgeName string
	mode       string
	brSubnet   *net.IPNet
	gatewayIP  net.IP
}

type driver struct {
	dockerer
	ovsdber
	ipAllocator *ipallocator.IPAllocator
	version     string
	network     string
	cidr        *net.IPNet
	nameserver  string
	OvsdbNotifier
	pluginConfig
}

func (driver *driver) Listen(socket string) error {
	router := mux.NewRouter()
	router.NotFoundHandler = http.HandlerFunc(notFound)

	router.Methods("GET").Path("/status").HandlerFunc(driver.status)
	router.Methods("POST").Path("/Plugin.Activate").HandlerFunc(driver.handshake)
	router.Methods("POST").Path("/NetworkDriver.GetCapabilities").HandlerFunc(driver.capabilities)

	handleMethod := func(method string, h http.HandlerFunc) {
		router.Methods("POST").Path(fmt.Sprintf("/%s.%s", MethodReceiver, method)).HandlerFunc(h)
	}

	handleMethod("CreateNetwork", driver.createNetwork)
	handleMethod("DeleteNetwork", driver.deleteNetwork)
	handleMethod("CreateEndpoint", driver.createEndpoint)
	handleMethod("DeleteEndpoint", driver.deleteEndpoint)
	handleMethod("EndpointOperInfo", driver.infoEndpoint)
	handleMethod("Join", driver.joinEndpoint)
	handleMethod("Leave", driver.leaveEndpoint)

	var (
		listener net.Listener
		err      error
	)

	listener, err = net.Listen("unix", socket)
	if err != nil {
		return err
	}

	return http.Serve(listener, router)
}

func notFound(w http.ResponseWriter, r *http.Request) {
	log.Warnf("plugin Not found: [ %+v ]", r)
	http.NotFound(w, r)
}

func sendError(w http.ResponseWriter, msg string, code int) {
	log.Errorf("%d %s", code, msg)
	http.Error(w, msg, code)
}

func errorResponsef(w http.ResponseWriter, fmtString string, item ...interface{}) {
	json.NewEncoder(w).Encode(map[string]string{
		"Err": fmt.Sprintf(fmtString, item...),
	})
}

func objectResponse(w http.ResponseWriter, obj interface{}) {
	if err := json.NewEncoder(w).Encode(obj); err != nil {
		sendError(w, "Could not JSON encode response", http.StatusInternalServerError)
		return
	}
}

func emptyResponse(w http.ResponseWriter) {
	json.NewEncoder(w).Encode(map[string]string{})
}

type handshakeResp struct {
	Implements []string
}

func (driver *driver) handshake(w http.ResponseWriter, r *http.Request) {
	err := json.NewEncoder(w).Encode(&handshakeResp{
		[]string{"NetworkDriver"},
	})
	if err != nil {
		log.Fatalf("handshake encode: %s", err)
		sendError(w, "encode error", http.StatusInternalServerError)
		return
	}
	log.Debug("Handshake completed")
}

type capabilitiesResp struct {
	Scope string
}

func (driver *driver) capabilities(w http.ResponseWriter, r *http.Request) {
	err := json.NewEncoder(w).Encode(&capabilitiesResp{
		"local",
	})
	if err != nil {
		log.Fatalf("capabilities encode: %s", err)
		sendError(w, "encode error", http.StatusInternalServerError)
		return
	}
	log.Debug("Capabilities exchange complete")
}

func (driver *driver) status(w http.ResponseWriter, r *http.Request) {
	io.WriteString(w, fmt.Sprintln("ovs plugin", driver.version))
}

type networkCreate struct {
	NetworkID string
	Options   map[string]interface{}
}

func (driver *driver) createNetwork(w http.ResponseWriter, r *http.Request) {
	var create networkCreate
	err := json.NewDecoder(r.Body).Decode(&create)
	if err != nil {
		sendError(w, "Unable to decode JSON payload: "+err.Error(), http.StatusBadRequest)
		return
	}

	if driver.network != "" {
		errorResponsef(w, "You get just one network, and you already made %s", driver.network)
		return
	}
	driver.network = create.NetworkID
	driver.ipAllocator.RequestIP(driver.pluginConfig.brSubnet, nil)

	emptyResponse(w)
}

type networkDelete struct {
	NetworkID string
}

func (driver *driver) deleteNetwork(w http.ResponseWriter, r *http.Request) {
	var delete networkDelete
	if err := json.NewDecoder(r.Body).Decode(&delete); err != nil {
		sendError(w, "Unable to decode JSON payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	log.Debugf("Delete network request: %+v", &delete)
	if delete.NetworkID != driver.network {
		log.Debugf("network not found: %+v", &delete)
		errorResponsef(w, "Network %s not found", delete.NetworkID)
		return
	}
	driver.network = ""
	err := driver.deleteBridge(bridgeName)
	if err != nil {
		log.Errorf("Deleting bridge:[ %s ] and network:[ %s ] failed: %s", bridgeName, bridgeSubnet, err)
	}
	emptyResponse(w)
	log.Infof("Destroy network %s", delete.NetworkID)
}

type endpointCreate struct {
	NetworkID  string
	EndpointID string
	Interface  *EndpointInterface
	Options    map[string]interface{}
}

// EndpointInterface represents an interface endpoint.
type EndpointInterface struct {
	Address     string
	AddressIPv6 string
	MacAddress  string
}

type InterfaceName struct {
	SrcName   string
	DstName   string
	DstPrefix string
}

type endpointResponse struct {
	Interface EndpointInterface
}

func (driver *driver) createEndpoint(w http.ResponseWriter, r *http.Request) {
	var create endpointCreate
	if err := json.NewDecoder(r.Body).Decode(&create); err != nil {
		sendError(w, "Unable to decode JSON payload: "+err.Error(), http.StatusBadRequest)
		return
	}

	// If the bridge has been created, an OVSDB port table row should exist
	exists, err := driver.ovsdber.portExists(bridgeName)
	if err != nil {
		log.Debugf("Error querying the ovsdb cache: %s", err)
	}

	// If the bridge does not exist create and assign an IP
	if !exists {
		err := driver.setupBridge()
		if err != nil {
			log.Errorf("unable to setup the OVS bridge [ %s ]: %s ", bridgeName, err)
		}
	} else {
		log.Debugf("OVS bridge [ %s ] already exists, verifying its configuration.", bridgeName)
		driver.verifyBridgeIp()
	}

	// Bring the bridge up
	err = driver.interfaceUP(bridgeName)
	if err != nil {
		log.Warnf("Error enabling  bridge IP: [ %s ]", err)
	}
	netID := create.NetworkID
	endID := create.EndpointID

	if netID != driver.network {
		log.Warnf("Network not found, [ %s ], plugin restarts are currently unsupported until netid queries are added. Please restart the docker daemon and plugin", netID)
		errorResponsef(w, "No such network %s", netID)
		return
	}
	// Request an IP address from libnetwork based on the cidr scope
	// TODO: Add a user defined static ip addr option
	allocatedIP, err := driver.ipAllocator.RequestIP(driver.pluginConfig.brSubnet, nil)
	if err != nil || allocatedIP == nil {
		log.Errorf("Unable to obtain an IP address from libnetwork ipam: %s", err)
		errorResponsef(w, "%s", err)
		return
	}

	// generate a mac address for the pending container
	mac := makeMac(allocatedIP)
	// Have to convert container IP to a string ip/mask format
	bridgeMask := strings.Split(driver.pluginConfig.brSubnet.String(), "/")
	containerAddress := allocatedIP.String() + "/" + bridgeMask[1]

	log.Infof("Allocated container IP: [ %s ]", allocatedIP.String())

	respIface := &EndpointInterface{
		Address:    containerAddress,
		MacAddress: mac,
	}
	resp := &endpointResponse{
		Interface: *respIface,
	}
	log.Debugf("Create endpoint response: %+v", resp)
	objectResponse(w, resp)

	if driver.pluginConfig.mode == modeNAT {
		err := driver.natOut()
		if err != nil {
			log.Errorf("Error setting NAT mode iptable rules for OVS bridge [ %s ]: %s ", driver.pluginConfig.mode, err)
		}
	}
	log.Debugf("Create endpoint %s %+v", endID, resp)
}

type endpointDelete struct {
	NetworkID  string
	EndpointID string
}

func (driver *driver) deleteEndpoint(w http.ResponseWriter, r *http.Request) {
	var delete endpointDelete
	if err := json.NewDecoder(r.Body).Decode(&delete); err != nil {
		sendError(w, "Could not decode JSON encode payload", http.StatusBadRequest)
		return
	}
	log.Debugf("Delete endpoint request: %+v", &delete)
	emptyResponse(w)
	// null check cidr in case driver restarted and doesnt know the network to avoid panic
	if driver.cidr == nil {
		return
	}
	// ReleaseIP releases an ip back to a network
	if err := driver.ipAllocator.ReleaseIP(driver.cidr, driver.cidr.IP); err != nil {
		log.Warnf("Error releasing IP: %s", err)
	}
	log.Debugf("Delete endpoint %s", delete.EndpointID)
}

type endpointInfoReq struct {
	NetworkID  string
	EndpointID string
}

type endpointInfo struct {
	Value map[string]interface{}
}

func (driver *driver) infoEndpoint(w http.ResponseWriter, r *http.Request) {
	var info endpointInfoReq
	if err := json.NewDecoder(r.Body).Decode(&info); err != nil {
		sendError(w, "Could not decode JSON encode payload", http.StatusBadRequest)
		return
	}
	log.Debugf("Endpoint info request: %+v", &info)
	objectResponse(w, &endpointInfo{Value: map[string]interface{}{}})
	log.Debugf("Endpoint info %s", info.EndpointID)
}

type joinInfo struct {
	InterfaceName *InterfaceName
	Gateway       string
	GatewayIPv6   string
}

type join struct {
	NetworkID  string
	EndpointID string
	SandboxKey string
	Options    map[string]interface{}
}

type staticRoute struct {
	Destination string
	RouteType   int
	NextHop     string
}

type joinResponse struct {
	Gateway       string
	InterfaceName InterfaceName
	StaticRoutes  []*staticRoute
}

func (driver *driver) joinEndpoint(w http.ResponseWriter, r *http.Request) {
	var j join
	if err := json.NewDecoder(r.Body).Decode(&j); err != nil {
		sendError(w, "Could not decode JSON encode payload", http.StatusBadRequest)
		return
	}
	log.Debugf("Join request: %+v", &j)

	endID := j.EndpointID
	// create and attach local name to the bridge
	local := vethPair(endID[:5])
	if err := netlink.LinkAdd(local); err != nil {
		log.Errorf("failed to create the veth pair named: [ %v ] error: [ %s ] ", local, err)
		errorResponsef(w, "could not create veth pair")
		return
	}
	// Bring the veth pair up
	err := netlink.LinkSetUp(local)
	if err != nil {
		log.Warnf("Error enabling  Veth local iface: [ %v ]", local)
	}
	err = driver.addPortExec(bridgeName, local.Name)
	if err != nil {
		log.Errorf("error attaching veth [ %s ] to bridge [ %s ]", local.Name, bridgeName)
		errorResponsef(w, "%s", err)
	}
	log.Infof("Attached veth [ %s ] to bridge [ %s ]", local.Name, bridgeName)

	// SrcName gets renamed to DstPrefix on the container iface
	ifname := &InterfaceName{
		SrcName:   local.PeerName,
		DstPrefix: containerEthName,
	}
	res := &joinResponse{
		InterfaceName: *ifname,
		Gateway:       driver.pluginConfig.gatewayIP.String(),
	}

	defer objectResponse(w, res)
	log.Debugf("Join endpoint %s:%s to %s", j.NetworkID, j.EndpointID, j.SandboxKey)
}

// Create veth pair. Peername is renamed to eth0 in the container
func vethPair(suffix string) *netlink.Veth {
	return &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{Name: ovsPortPrefix + suffix},
		PeerName:  "ethc" + suffix,
	}
}

type leave struct {
	NetworkID  string
	EndpointID string
	Options    map[string]interface{}
}

func (driver *driver) leaveEndpoint(w http.ResponseWriter, r *http.Request) {
	var l leave
	if err := json.NewDecoder(r.Body).Decode(&l); err != nil {
		sendError(w, "Could not decode JSON encode payload", http.StatusBadRequest)
		return
	}
	log.Debugf("Leave request: %+v", &l)
	local := vethPair(l.EndpointID[:5])
	if err := netlink.LinkDel(local); err != nil {
		log.Errorf("unable to delete veth on leave: %s", err)
	}
	portID := fmt.Sprintf(ovsPortPrefix + l.EndpointID[:5])
	err := driver.ovsdber.deletePort(bridgeName, portID)
	if err != nil {
		log.Errorf("OVS port [ %s ] delete transaction failed on bridge [ %s ] due to: %s", portID, bridgeName, err)
		return
	}
	log.Infof("Deleted OVS port [ %s ] from bridge [ %s ]", portID, bridgeName)
	emptyResponse(w)
	log.Debugf("Leave %s:%s", l.NetworkID, l.EndpointID)
}

// Enable a netlink interface
func (driver *driver) interfaceUP(name string) error {
	iface, err := netlink.LinkByName(name)
	if err != nil {
		log.Debugf("Error retrieving a link named [ %s ]", iface.Attrs().Name)
		return err
	}
	return netlink.LinkSetUp(iface)
}
