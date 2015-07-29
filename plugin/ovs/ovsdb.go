package ovs

import (
	"errors"
	"fmt"
	"net"
	"reflect"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/codegangsta/cli"
	"github.com/docker/libnetwork/ipallocator"
	"github.com/samalba/dockerclient"
	"github.com/socketplane/libovsdb"
)

const (
	localhost    = "127.0.0.1"
	ovsdbPort    = 6640
	contextKey   = "container_id"
	contextValue = "container_data"
	minMTU       = 68
	modeFlat     = "flat" // L2 driver mode
	modeNAT      = "nat"  // NAT hides backend bridge network
)

var (
	quit         chan bool
	update       chan *libovsdb.TableUpdates
	ovsdbCache   map[string]map[string]libovsdb.Row
	contextCache map[string]string
)

type ovsdber struct {
	ovsdb *libovsdb.OvsdbClient
}

type OvsdbNotifier struct {
}

func (o OvsdbNotifier) Update(context interface{}, tableUpdates libovsdb.TableUpdates) {
	populateCache(tableUpdates)
	update <- &tableUpdates
}
func (o OvsdbNotifier) Disconnected(ovsClient *libovsdb.OvsdbClient) {
}
func (o OvsdbNotifier) Locked([]interface{}) {
}
func (o OvsdbNotifier) Stolen([]interface{}) {
}
func (o OvsdbNotifier) Echo([]interface{}) {
}

func (ovsdber *ovsdber) initDBCache() {
	quit = make(chan bool)
	update = make(chan *libovsdb.TableUpdates)
	ovsdbCache = make(map[string]map[string]libovsdb.Row)

	// Register for ovsdb table notifications
	var notifier OvsdbNotifier
	ovsdber.ovsdb.Register(notifier)
	// Populate ovsdb cache for the default Open_vSwitch db
	initCache, err := ovsdber.ovsdb.MonitorAll("Open_vSwitch", "")
	if err != nil {
		log.Errorf("Error populating initial OVSDB cache: %s", err)
	}
	populateCache(*initCache)
	contextCache = make(map[string]string)
	populateContextCache(ovsdber.ovsdb)

	// async monitoring of the ovs bridge(s) for table updates
	go ovsdber.monitorDockerBridge(bridgeName)
	for ovsdber.getRootUUID() == "" {
		time.Sleep(time.Second * 1)
	}
}

func New(version string, ctx *cli.Context) (Driver, error) {
	docker, err := dockerclient.NewDockerClient("unix:///var/run/docker.sock", nil)
	if err != nil {
		return nil, fmt.Errorf("could not connect to docker: %s", err)
	}

	// initiate the ovsdb manager port binding
	ovsdb, err := libovsdb.Connect(localhost, ovsdbPort)
	if err != nil {
		return nil, fmt.Errorf("could not connect to openvswitch on port [ %d ]: %s", ovsdbPort, err)
	}

	// bind user defined flags to the plugin config
	if ctx.String("bridge-name") != "" {
		bridgeName = ctx.String("bridge-name")
	}

	// lower bound of v4 MTU is 68-bytes per rfc791
	if ctx.Int("mtu") >= minMTU {
		defaultMTU = ctx.Int("mtu")
	} else {
		log.Fatalf("The MTU value passed [ %d ] must be greater then [ %d ] bytes per rfc791", ctx.Int("mtu"), minMTU)
	}

	// Parse the container subnet
	containerGW, containerCidr, err := net.ParseCIDR(ctx.String("bridge-subnet"))
	if err != nil {
		log.Fatalf("Error parsing cidr from the subnet flag provided [ %s ]: %s", FlagBridgeSubnet, err)
	}

	// Update the cli.go global var with the network if user provided
	bridgeSubnet = containerCidr.String()

	switch ctx.String("mode") {
	/* [ flat ] mode */
	//Flat mode requires a gateway IP address is used just like any other
	//normal L2 domain. If no gateway is specified, we attempt to guess using
	//the first usable IP on the container subnet from the CLI argument.
	//Example "192.168.1.0/24" we guess at a gatway of "192.168.1.1".
	//Flat mode requires a bridge-subnet flag with a subnet from your existing network
	case modeFlat:
		ovsDriverMode = modeFlat
		if ctx.String("gateway") != "" {
			// bind the container gateway to the IP passed from the CLI
			cliGateway := net.ParseIP(ctx.String("gateway"))
			if cliGateway == nil {
				log.Fatalf("The IP passed with the [ gateway ] flag [ %s ] was not a valid address: %s", FlagGateway.Value, err)
			}
			containerGW = cliGateway
		} else {
			// if no gateway was passed, guess the first valid address on the container subnet
			containerGW = ipIncrement(containerGW)
		}
	/* [ nat ] mode */
	//If creating a private network that will be NATed on the OVS bridge via IPTables
	//it is not required to pass a subnet since in a single host scenario it is hidden
	//from the network once it is masqueraded via IP tables.
	case modeNAT, "":
		ovsDriverMode = modeNAT
		if ctx.String("gateway") != "" {
			// bind the container gateway to the IP passed from the CLI
			cliGateway := net.ParseIP(ctx.String("gateway"))
			if cliGateway == nil {
				log.Fatalf("The IP passed with the [ gateway ] flag [ %s ] was not a valid address: %s", FlagGateway.Value, err)
			}
			containerGW = cliGateway
		} else {
			// if no gateway was passed, guess the first valid address on the container subnet
			containerGW = ipIncrement(containerGW)
		}
	default:
		log.Fatalf("Invalid ovs mode supplied [ %s ]. The plugin currently supports two modes: [ %s ] or [ %s ]", ctx.String("mode"), modeFlat, modeNAT)
	}

	pluginOpts := &pluginConfig{
		mtu:        defaultMTU,
		bridgeName: bridgeName,
		mode:       ovsDriverMode,
		brSubnet:   containerCidr,
		gatewayIP:  containerGW,
	}
	// Leaving as info for now. Change to debug eventually
	log.Infof("Plugin configuration: \n %s", pluginOpts)

	ipAllocator := ipallocator.New()
	d := &driver{
		dockerer: dockerer{
			client: docker,
		},
		ovsdber: ovsdber{
			ovsdb: ovsdb,
		},
		ipAllocator:  ipAllocator,
		pluginConfig: *pluginOpts,
		version:      version,
	}
	// Initialize ovsdb cache at rpc connection setup
	d.ovsdber.initDBCache()
	return d, nil
}

func populateContextCache(ovs *libovsdb.OvsdbClient) {
	if ovs == nil {
		return
	}
	tableCache := getTableCache("Interface")
	for _, row := range tableCache {
		config, ok := row.Fields["other_config"]
		ovsMap := config.(libovsdb.OvsMap)
		otherConfig := map[interface{}]interface{}(ovsMap.GoMap)
		if ok {
			containerID, ok := otherConfig[contextKey]
			if ok {
				contextCache[containerID.(string)] = otherConfig[contextValue].(string)
			}
		}
	}
}

func getTableCache(tableName string) map[string]libovsdb.Row {
	return ovsdbCache[tableName]
}

func (ovsdber *ovsdber) portExists(portName string) (bool, error) {
	condition := libovsdb.NewCondition("name", "==", portName)
	selectOp := libovsdb.Operation{
		Op:    "select",
		Table: "Port",
		Where: []interface{}{condition},
	}
	operations := []libovsdb.Operation{selectOp}
	reply, _ := ovsdber.ovsdb.Transact("Open_vSwitch", operations...)

	if len(reply) < len(operations) {
		return false, errors.New("Number of Replies should be atleast equal to number of Operations")
	}

	if reply[0].Error != "" {
		errMsg := fmt.Sprintf("Transaction Failed due to an error: %v", reply[0].Error)
		return false, errors.New(errMsg)
	}

	if len(reply[0].Rows) == 0 {
		return false, nil
	}
	return true, nil
}

func (ovsdber *ovsdber) monitorDockerBridge(brName string) {
	for {
		select {
		case currUpdate := <-update:
			for table, tableUpdate := range currUpdate.Updates {
				if table == "Bridge" {
					for _, row := range tableUpdate.Rows {
						empty := libovsdb.Row{}
						if !reflect.DeepEqual(row.New, empty) {
							oldRow := row.Old
							if _, ok := oldRow.Fields["name"]; ok {
								name := oldRow.Fields["name"].(string)
								if name == brName {
									ovsdber.createOvsdbBridge(name)
								}
							}
						}
					}
				}
			}
		}
	}
}

func (ovsdber *ovsdber) getRootUUID() string {
	for uuid := range ovsdbCache["Open_vSwitch"] {
		return uuid
	}
	return ""
}

func populateCache(updates libovsdb.TableUpdates) {
	for table, tableUpdate := range updates.Updates {
		if _, ok := ovsdbCache[table]; !ok {
			ovsdbCache[table] = make(map[string]libovsdb.Row)
		}
		for uuid, row := range tableUpdate.Rows {
			empty := libovsdb.Row{}
			if !reflect.DeepEqual(row.New, empty) {
				ovsdbCache[table][uuid] = row.New
			} else {
				delete(ovsdbCache[table], uuid)
			}
		}
	}
}

// return string representation of pluginConfig for debugging
func (d *pluginConfig) String() string {
	str := fmt.Sprintf(" container subnet: [%s]\n", d.brSubnet.String())
	str = str + fmt.Sprintf("    container gateway: [%s]\n", d.gatewayIP.String())
	str = str + fmt.Sprintf("    bridge name: [%s]\n", d.bridgeName)
	str = str + fmt.Sprintf("    bridge mode: [%s]\n", d.mode)
	str = str + fmt.Sprintf("    mtu: [%d]", d.mtu)
	return str
}
