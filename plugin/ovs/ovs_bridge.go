package ovs

import (
	"errors"
	"fmt"
	"net"

	log "github.com/Sirupsen/logrus"
	"github.com/socketplane/libovsdb"
)

//  setupBridge If bridge does not exist create it.
func (driver *driver) setupBridge() error {
	if err := driver.ovsdber.addBridge(bridgeName); err != nil {
		log.Errorf("error creating ovs bridge [ %s ] : [ %s ]", bridgeName, err)
		return err
	}
	// Set bridge IP.
	brAddr, _, err := net.ParseCIDR(bridgeIfaceNet)
	if err != nil {
		log.Warnf("Error parsing cidr from the default subnet: %s", err)
	}
	// Set the L3 IP addr on the bridge netlink iface
	if bridgeIfaceNet != "" {
		err := driver.setInterfaceIP(bridgeName, bridgeIfaceNet)
		// if the bridge IP and it is the gateway log an error. TODO: Check if the brIP exists already
		if err != nil && gatewayIP == brAddr.String() {
			log.Debugf("Error assigning address : [ %s ] on bridge: [ %s ]  with an error of: %s", bridgeSubnet, bridgeName, err)
		}
	}
	// Verify there is an IP on the netlink iface. If it is the gateway it is a problem.
	brNet, err := getIfaceAddr(bridgeName)
	if err != nil {
		log.Warnf("No IP address found on bridge: [ %s ]: %s", bridgeName, err)
	} else {
		log.Debugf("IP address [ %s ] found on bridge: [ %s ]", brNet, bridgeName)
	}
	return nil
}

// verifyBridge is if the bridge already existed and ensures it has a netlink L3 IP
func (driver *driver) verifyBridge() error {
	// Verify there is an IP on the bridge
	brNet, err := getIfaceAddr(bridgeName)
	if err == nil {
		log.Debugf("IP address [ %s ] found on bridge: [ %s ]", brNet, bridgeName)
		return nil
	}
	// Parse the bridge IP.
	brAddr, _, err := net.ParseCIDR(bridgeSubnet)
	if err != nil {
		log.Errorf("Error parsing cidr from the default subnet: %s", err)
		return err
	}
	if bridgeIfaceNet != "" {
		err := driver.setInterfaceIP(bridgeName, bridgeIfaceNet)
		// if the there is an error setting the br IP and it is the gateway return an err
		if err != nil && gatewayIP == brAddr.String() {
			log.Warnf("Error assigning address : [ %s ] on bridge: [ %s ]  with an error of: %s", bridgeSubnet, bridgeName, err)
		}
	}
	return nil
}

func (ovsdber *ovsdber) createBridgeIface(name string) error {
	err := ovsdber.createOvsdbBridge(name)
	if err != nil {
		log.Errorf("Bridge creation failed for the bridge named [ %s ] with errors: %s", name, err)
	}
	return nil
}

// createOvsdbBridge creates the OVS bridge
func (ovsdber *ovsdber) createOvsdbBridge(bridgeName string) error {
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
	bridge["stp_enable"] = false
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
	condition := libovsdb.NewCondition("_uuid", "==", libovsdb.UUID{ovsdber.getRootUUID()})

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

// Check if port exists prior to creating a bridge
func (ovsdber *ovsdber) addBridge(bridgeName string) error {
	if ovsdber.ovsdb == nil {
		return errors.New("OVS not connected")
	}
	// If the bridge has been created, an internal port with the same name will exist
	exists, err := ovsdber.portExists(bridgeName)
	if err != nil {
		return err
	}
	if !exists {
		if err := ovsdber.createBridgeIface(bridgeName); err != nil {
			return err
		}
		exists, err = ovsdber.portExists(bridgeName)
		if err != nil {
			return err
		}
		if !exists {
			return errors.New("Error creating Bridge")
		}
	}
	return nil
}

// deleteBridge deletes the OVS bridge
func (driver *driver) deleteBridge(bridgeName string) error {
	namedBridgeUUID := "bridge"

	// simple delete operation
	condition := libovsdb.NewCondition("name", "==", bridgeName)
	deleteOp := libovsdb.Operation{
		Op:    "delete",
		Table: "Bridge",
		Where: []interface{}{condition},
	}

	// Deleting a Bridge row in Bridge table requires mutating the open_vswitch table.
	mutateUUID := []libovsdb.UUID{libovsdb.UUID{namedBridgeUUID}}
	mutateSet, _ := libovsdb.NewOvsSet(mutateUUID)
	mutation := libovsdb.NewMutation("bridges", "delete", mutateSet)

	// simple mutate operation
	mutateOp := libovsdb.Operation{
		Op:        "mutate",
		Table:     "Open_vSwitch",
		Mutations: []interface{}{mutation},
		Where:     []interface{}{condition},
	}

	operations := []libovsdb.Operation{deleteOp, mutateOp}
	reply, _ := driver.ovsdber.ovsdb.Transact("Open_vSwitch", operations...)

	if len(reply) < len(operations) {
		log.Error("Number of Replies should be atleast equal to number of Operations")
	}
	for i, o := range reply {
		if o.Error != "" && i < len(operations) {
			log.Error("Transaction Failed due to an error :", o.Error, " in ", operations[i])
			errMsg := fmt.Sprintf("Transaction Failed due to an error: %s in operation: %v", o.Error, operations[i])
			return errors.New(errMsg)
		} else if o.Error != "" {
			errMsg := fmt.Sprintf("Transaction Failed due to an error : %s", o.Error)
			return errors.New(errMsg)
		}
	}
	log.Debugf("OVSDB delete bridge transaction succesful")
	return nil
}
