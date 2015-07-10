package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path"
	"runtime"
	"strconv"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/docker/libnetwork/ipallocator"
	"github.com/gorilla/mux"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

const (
	MethodReceiver = "NetworkDriver"
	version        = "0.1"
	//	bridgeName = "ovsbr-docker0"  // temp
	//	bridgeSubnet = "172.18.40.0/24" // temp
	//	bridgeIP = "172.18.40.1/24" // temp
	defaultRoute = "0.0.0.0/0"
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

// Struct for binding bridge options CLI flags
type bridgeOpts struct {
	brName   string
	brSubnet net.IPNet
	brIP     net.IPNet
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

	driver.ovsdber.initDBCache()
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

	_, ipNet, err := net.ParseCIDR(bridgeSubnet)
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

	log.Debugf("Libnetwork IPAM allocated container IP: [ %s ]", allocatedIP.String())

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
	log.Infof("Delete endpoint request: %+v", &delete)
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

	//	driver.ovsdber.initDBCache()
	// If the bridge has been created, a port with the same name should exist
	exists, err := driver.ovsdber.portExists(bridgeName)
	if err != nil {
		log.Debugf("Port exists error: %s", err)
	}
	if !exists {
		// If bridge does not exist create it
		if err := driver.ovsdber.createBridge(bridgeName); err != nil {
			log.Warnf("error creating ovs bridge [ %s ] : [ %s ]", bridgeName, err)
		}
		// TODO: check if exists to get rid of file exists log.
		// Set bridge IP.
		err = driver.setInterfaceIP(bridgeName, bridgeIP)
		if err != nil {
			log.Debugf("Error assigning address : [ %s ] on bridge: [ %s ]  with an error of: %s", bridgeSubnet, bridgeName, err)
		}
		// Bring the bridge up
		err = driver.ifaceUP(bridgeName)
		if err != nil {
			log.Warnf("Error enabling  bridge IP: [ %s ]", err)
		}
		// Verify there is an IP on the bridge
		brNet, err := getIfaceAddr(bridgeName)
		if err != nil {
			log.Warnf("No IP address found on bridge: [ %s ]: %s", bridgeName, err)
		} else {
			log.Debugf("IP address [ %s ] found on bridge: [ %s ]", brNet, bridgeName)
		}
	}

	// generate a port name eth0-a42a9, unique per bridge since OVS internal ports can't be renamed
	endID := fmt.Sprintf("eth0-" + j.EndpointID[:5])
	// Create an OVS port that the container will use as its iface
	portID, err := driver.ovsdber.createOvsInternalPort(endID, bridgeName, 0)
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

	// TODO: trying to set the GW fails with a dest unreachable
	// Im guessing it is a timing issue of the internal OVS port
	// move to the container netns. The connected and gateway routes
	// are added via netlink directly from the plugin itself atm.
	res := &joinResponse{
		InterfaceNames: []*iface{ifname},
		// Gateway: "not worky, setting df GW in methods below using netlink",
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
	// get the nspid for post container route additions
	_, nspid := path.Split(j.SandboxKey)
	go driver.addRoutes(portID, nspid)
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

	portID := fmt.Sprintf("eth0-" + l.EndpointID[:5])
	log.Debugf("Attempting to delete ovs port [ %s ] from bridge [ %s ]", portID, bridgeName)
	err := driver.ovsdber.deletePort(bridgeName, portID)
	if err != nil {
		log.Errorf("delete port transaction failed due to:  [ %s ]", err)
		return
	}
	// todo: clean up interface
	// local := vethPair(l.EndpointID[:5])
	// if err := netlink.LinkDel(local); err != nil {
	// 	Warning.Printf("unable to delete veth on leave: %s", err)
	// }
	emptyResponse(w)
	log.Infof("Leave %s:%s", l.NetworkID, l.EndpointID)
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

// Add the local connected route for the container NS
func (driver *driver) addRoutes(portID, nsPid string) {
	// if the local connected network doesnt exist yet, the gateway is rejected
	// with a no route to gw. TODO: see why connected route isnt created by libnet
	err := setConnectedRoute(portID, nsPid)
	if err != nil {
		log.Errorf("Errors encountered adding routes to the port [ %s ]: %s", portID, err)
	}
}

// Add the default gateway to the container NS if one exists.
// Default to the OVS bridge it is attached to if it has an IP?
func setConnectedRoute(ifaceName string, nsPid string) error {
	// Lock the OS thread
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	origns, err := netns.Get()
	if err != nil {
		return err
	}
	defer origns.Close()

	// Have to wait for the filehandle to be craeted by libnetwork
	time.Sleep(time.Second * 1)
	dockerNsFd, err := netns.GetFromDocker(nsPid)
	if err != nil {
		log.Errorf("Failed to get the nspid from docker %s", err)
	}
	// switch to container namespace
	err = netns.Set(dockerNsFd)
	if err != nil {
		return err
	}
	defer dockerNsFd.Close()
	iface, err := netlink.LinkByName(ifaceName)
	if err != nil {
		return err
	}
	nwAddr, dst, err := net.ParseCIDR(bridgeSubnet)
	if err != nil {
		return err
	}
	// create a connected route for the interface. Something is squirrely.
	connectedRoute := &netlink.Route{
		LinkIndex: iface.Attrs().Index,
		Dst:       dst,
		Scope:     netlink.SCOPE_UNIVERSE,
	}
	log.Debugf("Adding conected route [ %+v ]", connectedRoute)
	err = netlink.RouteAdd(connectedRoute)
	if err != nil {
		log.Errorf("Failed to add connected route [ %+v ]: %s", nwAddr, connectedRoute, err)
		return err
	}
	_, dfDst, err := net.ParseCIDR(defaultRoute)
	if err != nil {
		return err
	}
	gwRoutes, err := netlink.RouteGet(nwAddr)
	if err != nil {
		return fmt.Errorf("Route for the gateway could not be found: %v", err)
	}
	// create a connected route for the interface (todoL netlink doesnt seem to take GW)
	defaultRoute := &netlink.Route{
		LinkIndex: gwRoutes[0].LinkIndex,
		Scope:     netlink.SCOPE_UNIVERSE,
		Dst:       dfDst,
	}
	log.Debugf("Adding default route [ %+v ]", defaultRoute)
	err = netlink.RouteAdd(defaultRoute)
	if err != nil {
		log.Errorf("Failed to set the default route [ %v ]: %s", defaultRoute, err)
		return err
	}
	defer netns.Set(origns)
	return nil
}

func getIfaceIndex(name string) (int, error) {
	iface, err := netlink.LinkByName(name)
	if err != nil {
		return -1, err
	}
	linkIndex := iface.Attrs().Index
	log.Warnf("The link index for the port named [ %s ] is [ %s ]", name, linkIndex)
	return linkIndex, nil
}
