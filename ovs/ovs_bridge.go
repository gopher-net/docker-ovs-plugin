package ovs

import (
	"errors"
	"fmt"

	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/docker/libnetwork/iptables"
	"github.com/socketplane/libovsdb"
)

//  setupBridge If bridge does not exist create it.
func (d *Driver) initBridge(id string) error {
	bridgeName := d.networks[id].BridgeName
	if err := d.ovsdber.addBridge(bridgeName); err != nil {
		log.Errorf("error creating ovs bridge [ %s ] : [ %s ]", bridgeName, err)
		return err
	}

	retries := 3
	found := false
	for i := 0; i < retries; i++ {
		if found = validateIface(bridgeName); found {
			break
		}
		log.Debugf("A link for the OVS bridge named [ %s ] not found, retrying in 2 seconds", bridgeName)
		time.Sleep(2 * time.Second)
	}
	if found == false {
		return fmt.Errorf("Could not find a link for the OVS bridge named %s", bridgeName)

	}

	bridgeMode := d.networks[id].Mode
	switch bridgeMode {
	case modeNAT:
		{
			gatewayIP := d.networks[id].Gateway + "/" + d.networks[id].GatewayMask
			if err := setInterfaceIP(bridgeName, gatewayIP); err != nil {
				log.Debugf("Error assigning address: %s on bridge: %s with an error of: %s", gatewayIP, bridgeName, err)
			}

			// Validate that the IPAddress is there!
			_, err := getIfaceAddr(bridgeName)
			if err != nil {
				log.Fatalf("No IP address found on bridge %s", bridgeName)
				return err
			}

			// Add NAT rules for iptables
			if err = natOut(gatewayIP); err != nil {
				log.Fatalf("Could not set NAT rules for bridge %s", bridgeName)
				return err
			}
		}

	case modeFlat:
		{
			//ToDo: Add NIC to the bridge
		}
	}

	// Bring the bridge up
	err := interfaceUp(bridgeName)
	if err != nil {
		log.Warnf("Error enabling bridge: [ %s ]", err)
		return err
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
func (ovsdber *ovsdber) deleteBridge(bridgeName string) error {
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
	reply, _ := ovsdber.ovsdb.Transact("Open_vSwitch", operations...)

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

// todo: reconcile with what libnetwork does and port mappings
func natOut(cidr string) error {
	masquerade := []string{
		"POSTROUTING", "-t", "nat",
		"-s", cidr,
		"-j", "MASQUERADE",
	}
	if _, err := iptables.Raw(
		append([]string{"-C"}, masquerade...)...,
	); err != nil {
		incl := append([]string{"-I"}, masquerade...)
		if output, err := iptables.Raw(incl...); err != nil {
			return err
		} else if len(output) > 0 {
			return &iptables.ChainError{
				Chain:  "POSTROUTING",
				Output: output,
			}
		}
	}
	return nil
}
