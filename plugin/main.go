package main

import (
	"fmt"
	"os"

	log "github.com/Sirupsen/logrus"
	"github.com/codegangsta/cli"
	"github.com/gopher-net/docker-ovs-plugin/plugin/ovs"
)

const (
	version    = "0.1"
	ovsSocket  = "ovs.sock"
	pluginPath = "/run/docker/plugins/"
)

func main() {

	var flagSocket = cli.StringFlag{
		Name:  "socket, s",
		Value: ovsSocket,
		Usage: "listening unix socket",
	}
	var flagDebug = cli.BoolFlag{
		Name:  "debug, d",
		Usage: "enable debugging",
	}
	app := cli.NewApp()
	app.Name = "don"
	app.Usage = "Docker Open vSwitch Networking"
	app.Version = "0.0.1"
	app.Flags = []cli.Flag{
		flagDebug,
		flagSocket,
		ovs.FlagBridgeSubnet,
		ovs.FlagIpVlanMode,
		ovs.FlagGateway,
		ovs.FlagMtu,
	}
	app.Action = Run
	app.Before = initEnv
	app.Run(os.Args)
}

func initEnv(ctx *cli.Context) error {
	socketFile := ctx.String("socket")
	// Default loglevel is Info
	if ctx.Bool("debug") {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetLevel(log.InfoLevel)
	}
	log.SetOutput(os.Stderr)
	initSock(socketFile)
	return nil
}

// Run initializes the driver
func Run(ctx *cli.Context) {
	var d ovs.Driver
	var err error
	if d, err = ovs.New(version, ctx); err != nil {
		log.Fatalf("unable to create driver: %s", err)
	}
	log.Info("OVS network driver initialized successfully")

	// concatenate the absolute path to the spec file handle
	absSocket := fmt.Sprint(pluginPath, ctx.String("socket"))
	if err := d.Listen(absSocket); err != nil {
		log.Fatal(err)
	}
}

// removeSock if an old filehandle exists remove it
func removeSock(absFile string) {
	err := os.RemoveAll(absFile)
	if err != nil {
		log.Fatalf("Unable to remove the old socket file [ %s ] due to: %s", absFile, err)
	}
}

// initSock create the plugin filepath if it does not already exist
func initSock(socketFile string) {
	if err := os.MkdirAll(pluginPath, 0755); err != nil && !os.IsExist(err) {
		log.Warnf("Could not create net plugin path directory: [ %s ]", err)
	}
	// concatenate the absolute path to the spec file handle
	absFile := fmt.Sprint(pluginPath, socketFile)
	// If the plugin socket file already exists, remove it.
	if _, err := os.Stat(absFile); err == nil {
		log.Debugf("socket file [ %s ] already exists, unlinking the old file handle..", absFile)
		removeSock(absFile)
	}
	log.Debugf("The plugin absolute path and handle is [ %s ]", absFile)
}
