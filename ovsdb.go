package main

import (
	"errors"
	"fmt"
	"reflect"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/docker/libnetwork/ipallocator"
	"github.com/samalba/dockerclient"
	"github.com/socketplane/libovsdb"
)

const (
	localhost     = "127.0.0.1"
	defaultBridge = "ovsbr-docker0"
	contextKey    = "container_id"
	contextValue  = "container_data"
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

// TODO add cidr back as a parameter
func (ovsdber *ovsdber) createBridge(bridgeName string) error {
	//func (ovsdber *ovsdber) createBridge(cidr *net.IPNet, bridgeName string) error {

	quit = make(chan bool)
	update = make(chan *libovsdb.TableUpdates)
	ovsdbCache = make(map[string]map[string]libovsdb.Row)

	// Register for ovsdb table notifications
	var notifier OvsdbNotifier
	ovsdber.ovsdb.Register(notifier)

	// Populate ovsdb cache for the default Open_vSwitch db
	initCache, _ := ovsdber.ovsdb.MonitorAll("Open_vSwitch", "")
	populateCache(*initCache)
	contextCache = make(map[string]string)
	populateContextCache(ovsdber.ovsdb)

	// async monitoring of the ovs bridge(s) for table updates
	go ovsdber.monitorDockerBridge(defaultBridge)
	for getRootUUID() == "" {
		time.Sleep(time.Second * 1)
	}
	err := ovsdber.addBridge()
	if err != nil {
		log.Errorf("%s", err)
	}
	return nil
}

func New(version string) (Driver, error) {
	docker, err := dockerclient.NewDockerClient("unix:///var/run/docker.sock", nil)
	if err != nil {
		return nil, fmt.Errorf("could not connect to docker: %s", err)
	}
	ovsdb, err := libovsdb.Connect(localhost, 6640)
	if err != nil {
		return nil, fmt.Errorf("could not connect to openvswitch: %s", err)
	}
	ipAllocator := ipallocator.New()
	return &driver{
		dockerer: dockerer{
			client: docker,
		},
		ovsdber: ovsdber{
			ovsdb: ovsdb,
		},
		ipAllocator: ipAllocator,
		version:     version,
	}, nil

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

func (ovsdber *ovsdber) addBridge() error {
	if ovsdber.ovsdb == nil {
		return errors.New("OVS not connected")
	}

	// If the bridge has been created, a port with the same name should exist
	exists, err := ovsdber.portExists(defaultBridge)
	if err != nil {
		return err
	}
	if !exists {
		if err := ovsdber.createBridgeIface(defaultBridge); err != nil {
			return err
		}
		exists, err = ovsdber.portExists(defaultBridge)
		if err != nil {
			return err
		}
		if !exists {
			return errors.New("Error creating Bridge")
		}
	}
	return nil
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

func (ovsdber *ovsdber) createBridgeIface(name string) error {
	err := ovsdber.createOvsBrOps(name)
	if err != nil {
		log.Errorf("Bridge creation failed for the bridge named [ %s ] with errors: %s", name, err)
	}
	time.Sleep(time.Second * 1)
	return nil
}

func (ovsdber *ovsdber) createOvsBrOps(bridgeName string) error {
	namedBridgeUUID := "bridge"
	namedPortUUID := "port"
	namedIntfUUID := "intf"

	// intf row to insert
	intf := make(map[string]interface{})
	intf["name"] = bridgeName
	intf["type"] = `internal`

	insertIntfOp := libovsdb.Operation{
		Op:       "insert",
		Table:    "Interface",
		Row:      intf,
		UUIDName: namedIntfUUID,
	}

	// Port row to insert
	port := make(map[string]interface{})
	port["name"] = bridgeName
	port["interfaces"] = libovsdb.UUID{namedIntfUUID}

	insertPortOp := libovsdb.Operation{
		Op:       "insert",
		Table:    "Port",
		Row:      port,
		UUIDName: namedPortUUID,
	}

	// Bridge row to insert
	bridge := make(map[string]interface{})
	bridge["name"] = bridgeName
	bridge["stp_enable"] = true
	bridge["ports"] = libovsdb.UUID{namedPortUUID}

	insertBridgeOp := libovsdb.Operation{
		Op:       "insert",
		Table:    "Bridge",
		Row:      bridge,
		UUIDName: namedBridgeUUID,
	}

	// Inserting a Bridge row in Bridge table requires mutating the open_vswitch table.
	mutateUUID := []libovsdb.UUID{libovsdb.UUID{namedBridgeUUID}}
	mutateSet, _ := libovsdb.NewOvsSet(mutateUUID)
	mutation := libovsdb.NewMutation("bridges", "insert", mutateSet)
	condition := libovsdb.NewCondition("_uuid", "==", libovsdb.UUID{getRootUUID()})

	// Mutate operation
	mutateOp := libovsdb.Operation{
		Op:        "mutate",
		Table:     "Open_vSwitch",
		Mutations: []interface{}{mutation},
		Where:     []interface{}{condition},
	}

	operations := []libovsdb.Operation{insertIntfOp, insertPortOp, insertBridgeOp, mutateOp}
	reply, _ := ovsdber.ovsdb.Transact("Open_vSwitch", operations...)

	if len(reply) < len(operations) {
		return errors.New("Number of Replies should be atleast equal to number of Operations")
	}
	for i, o := range reply {
		if o.Error != "" && i < len(operations) {
			return errors.New("Transaction Failed due to an error :" + o.Error + " details : " + o.Details)
		} else if o.Error != "" {
			return errors.New("Transaction Failed due to an error :" + o.Error + " details : " + o.Details)
		}
	}
	return nil
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
									ovsdber.createOvsBrOps(name)
								}
							}
						}
					}
				}
			}
		}
	}
}

func getRootUUID() string {
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
