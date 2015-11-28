package ovs

import (
	"errors"
	"fmt"

	log "github.com/Sirupsen/logrus"
	"github.com/socketplane/libovsdb"
)

func (ovsdber *ovsdber) createOvsInternalPort(prefix string, bridge string, tag uint) (port string, err error) {
	// if you desire a longer hash add using generateRandomName(prefix, 5)
	port = prefix
	if ovsdber.ovsdb == nil {
		err = errors.New("OVS not connected")
		return
	}

	ovsdber.addInternalPort(bridge, port, tag)
	return
}

func (ovsdber *ovsdber) addInternalPort(bridgeName string, portName string, tag uint) error {
	namedPortUUID := "port"
	namedIntfUUID := "intf"

	// intf row to insert
	intf := make(map[string]interface{})
	intf["name"] = portName
	intf["type"] = `internal`

	insertIntfOp := libovsdb.Operation{
		Op:       "insert",
		Table:    "Interface",
		Row:      intf,
		UUIDName: namedIntfUUID,
	}

	// port row to insert
	port := make(map[string]interface{})
	port["name"] = portName
	port["interfaces"] = libovsdb.UUID{namedIntfUUID}

	if tag != 0 {
		port["tag"] = tag
	}

	insertPortOp := libovsdb.Operation{
		Op:       "insert",
		Table:    "Port",
		Row:      port,
		UUIDName: namedPortUUID,
	}

	// Inserting a row in Port table requires mutating the bridge table.
	mutateUUID := []libovsdb.UUID{libovsdb.UUID{namedPortUUID}}
	mutateSet, _ := libovsdb.NewOvsSet(mutateUUID)
	mutation := libovsdb.NewMutation("ports", "insert", mutateSet)
	condition := libovsdb.NewCondition("name", "==", bridgeName)

	// Mutate operation
	mutateOp := libovsdb.Operation{
		Op:        "mutate",
		Table:     "Bridge",
		Mutations: []interface{}{mutation},
		Where:     []interface{}{condition},
	}

	operations := []libovsdb.Operation{insertIntfOp, insertPortOp, mutateOp}
	reply, _ := ovsdber.ovsdb.Transact("Open_vSwitch", operations...)
	if len(reply) < len(operations) {
		log.Error("Number of Replies should be atleast equal to number of Operations")
		return errors.New("Number of Replies should be atleast equal to number of Operations")
	}
	for i, o := range reply {
		if o.Error != "" && i < len(operations) {
			msg := fmt.Sprintf("Transaction Failed due to an error : %v details: %v in %v", o.Error, o.Details, operations[i])
			return errors.New(msg)
		} else if o.Error != "" {
			msg := fmt.Sprintf("Transaction Failed due to an error : %v", o.Error)
			return errors.New(msg)
		}
	}
	return nil
}

func (ovsdber *ovsdber) deletePort(bridgeName string, portName string) error {
	condition := libovsdb.NewCondition("name", "==", portName)
	deleteOp := libovsdb.Operation{
		Op:    "delete",
		Table: "Port",
		Where: []interface{}{condition},
	}

	portUUID := portUUIDForName(portName)
	if portUUID == "" {
		log.Error("Unable to find a matching Port : ", portName)
		return fmt.Errorf("Unable to find a matching Port : [ %s ]", portName)
	}

	// Deleting a Bridge row in Bridge table requires mutating the open_vswitch table.
	mutateUUID := []libovsdb.UUID{libovsdb.UUID{portUUID}}
	mutateSet, _ := libovsdb.NewOvsSet(mutateUUID)
	mutation := libovsdb.NewMutation("ports", "delete", mutateSet)
	condition = libovsdb.NewCondition("name", "==", bridgeName)

	// simple mutate operation
	mutateOp := libovsdb.Operation{
		Op:        "mutate",
		Table:     "Bridge",
		Mutations: []interface{}{mutation},
		Where:     []interface{}{condition},
	}

	operations := []libovsdb.Operation{deleteOp, mutateOp}
	reply, _ := ovsdber.ovsdb.Transact("Open_vSwitch", operations...)

	if len(reply) < len(operations) {
		log.Error("Number of Replies should be atleast equal to number of Operations")
		return fmt.Errorf("Number of Replies should be atleast equal to number of Operations")
	}
	for i, o := range reply {
		if o.Error != "" && i < len(operations) {
			log.Error("Transaction Failed due to an error :", o.Error, " in ", operations[i])
			return fmt.Errorf("Transaction Failed due to an error: %s in %v", o.Error, operations[i])
		} else if o.Error != "" {
			log.Error("Transaction Failed due to an error :", o.Error)
			return fmt.Errorf("Transaction Failed due to an error %s", o.Error)
		}
	}
	return nil
}

func (ovsdber *ovsdber) addVxlanPort(bridgeName string, portName string, peerAddress string) {
	namedPortUUID := "port"
	namedIntfUUID := "intf"

	options := make(map[string]interface{})
	options["remote_ip"] = peerAddress

	// intf row to insert
	intf := make(map[string]interface{})
	intf["name"] = portName
	intf["type"] = `vxlan`
	intf["options"], _ = libovsdb.NewOvsMap(options)

	insertIntfOp := libovsdb.Operation{
		Op:       "insert",
		Table:    "Interface",
		Row:      intf,
		UUIDName: namedIntfUUID,
	}

	// port row to insert
	port := make(map[string]interface{})
	port["name"] = portName
	port["interfaces"] = libovsdb.UUID{namedIntfUUID}

	insertPortOp := libovsdb.Operation{
		Op:       "insert",
		Table:    "Port",
		Row:      port,
		UUIDName: namedPortUUID,
	}

	// Inserting a row in Port table requires mutating the bridge table.
	mutateUUID := []libovsdb.UUID{libovsdb.UUID{namedPortUUID}}
	mutateSet, _ := libovsdb.NewOvsSet(mutateUUID)
	mutation := libovsdb.NewMutation("ports", "insert", mutateSet)
	condition := libovsdb.NewCondition("name", "==", bridgeName)

	// simple mutate operation
	mutateOp := libovsdb.Operation{
		Op:        "mutate",
		Table:     "Bridge",
		Mutations: []interface{}{mutation},
		Where:     []interface{}{condition},
	}
	operations := []libovsdb.Operation{insertIntfOp, insertPortOp, mutateOp}
	reply, _ := ovsdber.ovsdb.Transact("Open_vSwitch", operations...)
	if len(reply) < len(operations) {
		fmt.Println("Number of Replies should be atleast equal to number of Operations")
	}
	for i, o := range reply {
		if o.Error != "" && i < len(operations) {
			fmt.Println("Transaction Failed due to an error :", o.Error, " details:", o.Details, " in ", operations[i])
		} else if o.Error != "" {
			fmt.Println("Transaction Failed due to an error :", o.Error)
		}
	}
}

// Silently fails :/
func (ovsdber *ovsdber) addOvsVethPort(bridgeName string, portName string, tag uint) error {

	namedPortUUID := "port"
	namedIntfUUID := "intf"

	// intf row to insert
	intf := make(map[string]interface{})
	intf["name"] = portName
	intf["type"] = `system`

	insertIntfOp := libovsdb.Operation{
		Op:       "insert",
		Table:    "Interface",
		Row:      intf,
		UUIDName: namedIntfUUID,
	}

	// port row to insert
	port := make(map[string]interface{})
	port["name"] = portName
	port["interfaces"] = libovsdb.UUID{namedIntfUUID}

	insertPortOp := libovsdb.Operation{
		Op:       "insert",
		Table:    "Port",
		Row:      port,
		UUIDName: namedPortUUID,
	}
	// Inserting a row in Port table requires mutating the bridge table.
	mutateUUID := []libovsdb.UUID{libovsdb.UUID{namedPortUUID}}
	mutateSet, _ := libovsdb.NewOvsSet(mutateUUID)
	mutation := libovsdb.NewMutation("ports", "insert", mutateSet)
	condition := libovsdb.NewCondition("name", "==", bridgeName)

	// Mutate operation
	mutateOp := libovsdb.Operation{
		Op:        "mutate",
		Table:     "Bridge",
		Mutations: []interface{}{mutation},
		Where:     []interface{}{condition},
	}
	operations := []libovsdb.Operation{insertIntfOp, insertPortOp, mutateOp}
	reply, _ := ovsdber.ovsdb.Transact("Open_vSwitch", operations...)

	if len(reply) < len(operations) {
		log.Error("Number of Replies should be atleast equal to number of Operations")
	}
	for i, o := range reply {
		if o.Error != "" && i < len(operations) {
			msg := fmt.Sprintf("Transaction Failed due to an error ]", o.Error, " details:", o.Details, " in ", operations[i])
			return errors.New(msg)
		} else if o.Error != "" {
			msg := fmt.Sprintf("Transaction Failed due to an error :", o.Error)
			return errors.New(msg)
		}
	}
	return nil
}

func portUUIDForName(portName string) string {
	portCache := ovsdbCache["Port"]
	for key, val := range portCache {
		if val.Fields["name"] == portName {
			return key
		}
	}
	return ""
}
