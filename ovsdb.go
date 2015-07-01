package main

import (
	"fmt"

	"github.com/docker/libnetwork/ipallocator"
	"github.com/samalba/dockerclient"
	"github.com/socketplane/libovsdb"
)

type ovsdber struct {
	client *libovsdb.OvsdbClient
}

func (ovsdber *ovsdber) createBridge(ovs *libovsdb.OvsdbClient, bridgeName string) error {

	bridge := make(map[string]interface{})
	bridge["name"] = bridgeName

	insertOp := libovsdb.Operation{
		Op:    "insert",
		Table: "Bridge",
		Row:   bridge,
	}

	mutateUuid := []libovsdb.UUID{libovsdb.UUID{namedUuid}}
	mutateSet, _ := libovsdb.NewOvsSet(mutateUuid)
	mutation := libovsdb.NewMutation("bridges", "insert", mutateSet)
	condition := libovsdb.NewCondition("_uuid", "==", libovsdb.UUID{getRootUuid()})

	mutateOp := libovsdb.Operation{
		Op:        "mutate",
		Table:     "Open_vSwitch",
		Mutations: []interface{}{mutation},
		Where:     []interface{}{condition},
	}

	operations := []libovsdb.Operation{insertOp, mutateOp}
	reply, _ := ovs.Transact("Open_vSwitch", operations...)

	if len(reply) < len(operations) {
		fmt.Println("Number of Replies should be atleast equal to number of Operations")
	}
	ok := true
	for i, o := range reply {
		if o.Error != "" && i < len(operations) {
			fmt.Println("Transaction Failed due to an error :", o.Error, " details:", o.Details, " in ", operations[i])
			ok = false
		} else if o.Error != "" {
			fmt.Println("Transaction Failed due to an error :", o.Error)
			ok = false
		}
	}
	if ok {
		fmt.Println("Bridge Addition Successful : ", reply[0].UUID.GoUuid)
	}
	return
}

func New(version string) (Driver, error) {
	docker, err := dockerclient.NewDockerClient("unix:///var/run/docker.sock", nil)
	if err != nil {
		return nil, fmt.Errorf("could not connect to docker: %s", err)
	}

	ovsdb, err := libovsdb.Connect("", 0)
	if err != nil {
		return nil, fmt.Errorf("could not connect to openvswitch: %s", err)
	}

	ipAllocator := ipallocator.New()

	return &driver{
		dockerer: dockerer{
			client: docker,
		},
		ovsdber: ovsdber{
			client: ovsdb,
		},
		ipAllocator: ipAllocator,
		version:     version,
	}, nil
}
