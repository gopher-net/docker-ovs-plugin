package ovs

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/socketplane/libovsdb"
)

const (
	localhost    = "127.0.0.1"
	ovsdbPort    = 6640
	contextKey   = "container_id"
	contextValue = "container_data"
	minMTU       = 68
)

var (
	quit         chan bool
	update       chan *libovsdb.TableUpdates
	ovsdbCache   *dbCache
	contextCache *ctxCache
)

type dbCache struct {
	sync.Mutex
	cache map[string]map[string]libovsdb.Row
}

type ctxCache struct {
	sync.Mutex
	cache map[string]string
}

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
	ovsdbCache = &dbCache{
		cache: make(map[string]map[string]libovsdb.Row),
	}
	contextCache = &ctxCache{
		cache: make(map[string]string),
	}

	// Register for ovsdb table notifications
	var notifier OvsdbNotifier
	ovsdber.ovsdb.Register(notifier)
	// Populate ovsdb cache for the default Open_vSwitch db
	initCache, err := ovsdber.ovsdb.MonitorAll("Open_vSwitch", "")
	if err != nil {
		log.Errorf("Error populating initial OVSDB cache: %s", err)
	}
	populateCache(*initCache)
	populateContextCache(ovsdber.ovsdb)

	// async monitoring of the ovs bridge(s) for table updates
	go ovsdber.monitorBridges()
	for ovsdber.getRootUUID() == "" {
		time.Sleep(time.Second * 1)
	}
}

func populateContextCache(ovs *libovsdb.OvsdbClient) {
	if ovs == nil {
		return
	}
	ovsdbCache.tableCache(`Interface`, func(tableCache map[string]libovsdb.Row) {
		for _, row := range tableCache {
			config, ok := row.Fields["other_config"]
			ovsMap := config.(libovsdb.OvsMap)
			otherConfig := map[interface{}]interface{}(ovsMap.GoMap)
			if ok {
				containerID, ok := otherConfig[contextKey]
				if ok {
					contextCache.put(containerID.(string), otherConfig[contextValue].(string))
				}
			}
		}
	})
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

func (ovsdber *ovsdber) monitorBridges() {
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
								ovsdber.createOvsdbBridge(name)
							}
						}
					}
				}
			}
		}
	}
}

func (ovsdber *ovsdber) getRootUUID() (r string) {
	ovsdbCache.tableCache(`Open_vSwitch`, func(mp map[string]libovsdb.Row) {
		for uuid := range mp {
			r = uuid
			return
		}
	})
	return
}

func populateCache(updates libovsdb.TableUpdates) {
	for table, tableUpdate := range updates.Updates {
		ovsdbCache.initTable(table)
		for uuid, row := range tableUpdate.Rows {
			empty := libovsdb.Row{}
			if !reflect.DeepEqual(row.New, empty) {
				ovsdbCache.addTableRow(table, uuid, row.New)
			} else {
				ovsdbCache.deleteTableRow(table, uuid)
			}
		}
	}
}

type rowfunc func(map[string]libovsdb.Row)

func (dbc *dbCache) tableCache(table string, rf rowfunc) {
	dbc.Lock()
	rf(dbc.cache[table])
	dbc.Unlock()
}

func (dbc *dbCache) initTable(key string) (added bool) {
	dbc.Lock()
	if _, ok := dbc.cache[key]; !ok {
		dbc.cache[key] = make(map[string]libovsdb.Row)
		added = true
	}
	dbc.Unlock()
	return
}

func (dbc *dbCache) addTableRow(table, uuid string, row libovsdb.Row) {
	dbc.Lock()
	tbl, ok := dbc.cache[table]
	if !ok {
		tbl = make(map[string]libovsdb.Row)
	}
	tbl[uuid] = row
	dbc.cache[table] = tbl
	dbc.Unlock()
}

func (dbc *dbCache) deleteTableRow(table, uuid string) {
	dbc.Lock()
	if tbl, ok := dbc.cache[table]; ok {
		delete(tbl, uuid)
		dbc.cache[table] = tbl
	}
	dbc.Unlock()
}

func (c *ctxCache) get(k string) (r string) {
	c.Lock()
	if c != nil && c.cache != nil {
		r = c.cache[k]
	}
	c.Unlock()
	return ``
}

func (c *ctxCache) put(k, v string) {
	c.Lock()
	if c != nil && c.cache != nil {
		c.cache[k] = v
	}
	c.Unlock()
}
