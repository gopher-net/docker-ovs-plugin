package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"

	log "github.com/Sirupsen/logrus"
	"github.com/docker/libnetwork/ipallocator"
	"github.com/gorilla/mux"
	"github.com/vishvananda/netlink"
	"strconv"
)

const (
	MethodReceiver = "NetworkDriver"
	version        = "0.1"
	defaultBridge  = "ovsbr-docker0"  // temp
	defaultSubnet  = "172.18.40.1/24" // temp
)

var bridgeNetworks []*net.IPNet

func init() {
	// Here we don't follow the convention of using the 1st IP of the range for the gateway.
	// This is to use the same gateway IPs as the /24 ranges, which predate the /16 ranges.
	// In theory this shouldn't matter - in practice there's bound to be a few scripts relying
	// on the internal addressing or other stupid things like that.
	// They shouldn't, but hey, let's not break them unless we really have to.
	// Don't use 172.16.0.0/16, it conflicts with EC2 DNS 172.16.0.23

	// 172.[17-31].42.1/16
	mask := []byte{255, 255, 0, 0}
	for i := 17; i < 32; i++ {
		bridgeNetworks = append(bridgeNetworks, &net.IPNet{IP: []byte{172, byte(i), 42, 1}, Mask: mask})
	}
	// 10.[0-255].42.1/16
	for i := 0; i < 256; i++ {
		bridgeNetworks = append(bridgeNetworks, &net.IPNet{IP: []byte{10, byte(i), 42, 1}, Mask: mask})
	}
	// 192.168.[42-44].1/24
	mask[2] = 255
	for i := 42; i < 45; i++ {
		bridgeNetworks = append(bridgeNetworks, &net.IPNet{IP: []byte{192, 168, byte(i), 1}, Mask: mask})
	}
}

type Driver interface {
	Listen(string) error
}

type driver struct {
	dockerer
	ovsdber
	ipAllocator *ipallocator.IPAllocator
	version     string
	network     string
	cidr        *net.IPNet
	nameserver  string
}

func (driver *driver) Listen(socket string) error {
	router := mux.NewRouter()
	router.NotFoundHandler = http.HandlerFunc(notFound)

	router.Methods("GET").Path("/status").HandlerFunc(driver.status)
	router.Methods("POST").Path("/Plugin.Activate").HandlerFunc(driver.handshake)

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
	log.Warnf("[plugin] Not found: %+v", r)
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
		log.Fatalf("handshake encode:", err)
		sendError(w, "encode error", http.StatusInternalServerError)
		return
	}
	log.Debug("Handshake completed")
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

	_, ipNet, err := net.ParseCIDR(defaultSubnet)
	if err != nil {
		log.Warnf("Error parsing cidr from the default subnet: %s", err)
	}
	cidr := ipNet
	driver.cidr = cidr
	driver.ipAllocator.RequestIP(cidr, nil)

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
	Debug.Printf("Delete network request: %+v", &delete)
	if delete.NetworkID != driver.network {
		errorResponsef(w, "Network %s not found", delete.NetworkID)
		return
	}
	driver.network = ""
	// todo: remove the bridge
	emptyResponse(w)
	Info.Printf("Destroy network %s", delete.NetworkID)
}

type endpointCreate struct {
	NetworkID  string
	EndpointID string
	Interfaces []*iface
	Options    map[string]interface{}
}

type iface struct {
	ID         int
	SrcName    string
	DstPrefix  string
	Address    string
	MacAddress string
}

type endpointResponse struct {
	Interfaces []*iface
}

func (driver *driver) createEndpoint(w http.ResponseWriter, r *http.Request) {
	var create endpointCreate
	if err := json.NewDecoder(r.Body).Decode(&create); err != nil {
		sendError(w, "Unable to decode JSON payload: "+err.Error(), http.StatusBadRequest)
		return
	}

	netID := create.NetworkID
	endID := create.EndpointID

	if netID != driver.network {
		log.Info("60-driver-createNetwork driver")
		errorResponsef(w, "No such network %s", netID)
		return
	}

	// Request an IP address from libnetwork based on the cidr scope
	// TODO: Add a user defined static ip addr option
	allocatedIP, err := driver.ipAllocator.RequestIP(driver.cidr, nil)
	if err != nil || allocatedIP == nil {
		log.Errorf("Unable to obtain an IP address from libnetwork ipam: ", err)
		errorResponsef(w, "%s", err)
		return
	}

	mac := makeMac(allocatedIP)
	// Have to convert container IP to a string ip/mask format
	_, containerMask := driver.cidr.Mask.Size()
	containerAddress := allocatedIP.String() + "/" + strconv.Itoa(containerMask)

	log.Debugf("Dynamically allocated container IP is [ %s ]", allocatedIP.String())

	respIface := &iface{
		Address:    containerAddress,
		MacAddress: mac,
	}

	resp := &endpointResponse{
		Interfaces: []*iface{respIface},
	}

	objectResponse(w, resp)
	log.Infof("Create endpoint %s %+v", endID, resp)
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
	Debug.Printf("Delete endpoint request: %+v", &delete)
	emptyResponse(w)

	// ReleaseIP releases an ip back to a network
	if err := driver.ipAllocator.ReleaseIP(driver.cidr, driver.cidr.IP); err != nil {
		log.Warnf("error releasing IP: %s", err)
	}
	log.Infof("Delete endpoint %s", delete.EndpointID)
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
	log.Infof("Endpoint info request: %+v", &info)
	objectResponse(w, &endpointInfo{Value: map[string]interface{}{}})
	log.Infof("Endpoint info %s", info.EndpointID)
}

type joinInfo struct {
	InterfaceNames []*iface
	Gateway        string
	GatewayIPv6    string
	HostsPath      string
	ResolvConfPath string
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
	InterfaceID int
}

type joinResponse struct {
	HostsPath      string
	ResolvConfPath string
	Gateway        string
	InterfaceNames []*iface
	StaticRoutes   []*staticRoute
}

func (driver *driver) joinEndpoint(w http.ResponseWriter, r *http.Request) {
	var j join
	if err := json.NewDecoder(r.Body).Decode(&j); err != nil {
		sendError(w, "Could not decode JSON encode payload", http.StatusBadRequest)
		return
	}
	log.Debugf("Join request: %+v", &j)
	// If the bridge has been created, a port with the same name should exist
	exists, err := driver.ovsdber.portExists(defaultBridge)
	if err != nil {
		log.Debugf("Port exists error: %s", err)
	}
	if !exists {
		// If bridge does not exist create it
		if err := driver.ovsdber.createBridge(defaultBridge); err != nil {
			log.Warnf("error creating ovs bridge [ %s ] : [ %s ]", defaultBridge, err)
		}
	}
	// Set bridge IP.
	// TODO: check if exists to get rid of file exists log.
	err = driver.setInterfaceIP(defaultBridge, defaultSubnet)
	if err != nil {
		log.Warnf("Error setting subnet: [ %s ] on bridge: [ %s ]  with an error of: %s", defaultSubnet, defaultBridge, err)
	}
	// Bring the bridge up
	err = driver.ifaceUP(defaultBridge)
	if err != nil {
		log.Warnf("Error enabling  bridge IP: [ %s ]", err)
	}
	// Verify there is an IP on the bridge
	brNet, err := GetIfaceAddr(defaultBridge)
	if err != nil {
		log.Warnf("No IP address found on bridge: [ %s ]: %s", defaultBridge, err)
	} else {
		log.Debugf("IP address [ %s ] found on bridge: [ %s ]", brNet, defaultBridge)
	}

	endID := j.EndpointID
	// Create an OVS port that the container will use as its iface
	portID, err := driver.ovsdber.createOvsInternalPort(endID[:5], defaultBridge, 0)
	if err != nil {
		log.Debugf("error creating OVS port [ %s ]", portID)
		errorResponsef(w, "%s", err)
	}
	log.Debugf("Port ID is [ %s ]", portID)

	ifname := &iface{
		SrcName:   portID,
		DstPrefix: portID,
		ID:        0,
	}

	// TODO: debug gateway failure
	res := &joinResponse{
		InterfaceNames: []*iface{ifname},
		// Gateway: "172.18.40.1",
	}

	// todo: are we interested in the nameserver code?
	//	if driver.nameserver != "" {
	//		routeToDNS := &staticRoute{
	//			Destination: driver.nameserver + "/32",
	//			RouteType:   types.CONNECTED,
	//			NextHop:     "",
	//			InterfaceID: 0,
	//		}
	//		res.StaticRoutes = []*staticRoute{routeToDNS}
	//	}

	objectResponse(w, res)
	log.Infof("Join endpoint %s:%s to %s", j.NetworkID, j.EndpointID, j.SandboxKey)
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

	// todo: clean up interface
	// local := vethPair(l.EndpointID[:5])
	// if err := netlink.LinkDel(local); err != nil {
	// 	Warning.Printf("unable to delete veth on leave: %s", err)
	// }
	emptyResponse(w)
	log.Infof("Leave %s:%s", l.NetworkID, l.EndpointID)
}

// Return the IPv4 address of a network interface
func GetIfaceAddr(name string) (*net.IPNet, error) {
	iface, err := netlink.LinkByName(name)
	if err != nil {
		return nil, err
	}
	addrs, err := netlink.AddrList(iface, netlink.FAMILY_V4)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("Interface %v has no IP addresses", name)
	}
	if len(addrs) > 1 {
		log.Infof("Interface [ %v ] has more than 1 IPv4 address. Defaulting to using [ %v ]\n", name, addrs[0].IP)
	}
	return addrs[0].IPNet, nil
}

func (driver *driver) setInterfaceIP(name string, rawIP string) error {
	iface, err := netlink.LinkByName(name)
	if err != nil {
		return err
	}

	ipNet, err := netlink.ParseIPNet(rawIP)
	if err != nil {
		return err
	}
	addr := &netlink.Addr{ipNet, ""}
	return netlink.AddrAdd(iface, addr)
}

func (driver *driver) ifaceUP(name string) error {
	iface, err := netlink.LinkByName(name)
	if err != nil {
		return err
	}
	return netlink.LinkSetUp(iface)
}
