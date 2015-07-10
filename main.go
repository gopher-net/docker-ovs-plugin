package main

import (
	"os"
	"path/filepath"

	log "github.com/Sirupsen/logrus"
	"github.com/codegangsta/cli"
)

var (
	// TODO: Values need to be bound to driver. Need to modify the Driver iface. Added brOpts if we want to pass that to Listen(string)
	flagBridgeName   = cli.StringFlag{Name: "bridge-name", Value: bridgeName, Usage: "name of the OVS bridge to add containers. If it doees not exist, it will be created. default: --bridge-name=ovsbr-docker0"}
	flagBridgeIP     = cli.StringFlag{Name: "bridge-ip", Value: bridgeIP, Usage: "IP and netmask of the bridge. default: --bridge-ip=172.18.40.1/24"}
	flagBridgeSubnet = cli.StringFlag{Name: "bridge-subnet", Value: bridgeSubnet, Usage: "subnet for the containers on the bridge to use (currently IPv4 support). default: --bridge-subnet=172.18.40.0/24"}
)

var (
	// TODO: Should we use CLI flags, ENVs or dnet-ctl for bridge properties.
	bridgeName   = "ovsbr-docker0"  // temp until binding via flags
	bridgeSubnet = "172.18.40.0/24" // temp until binding via flags
	bridgeIP     = "172.18.40.1/24" // temp until binding via flags
)

func main() {

	var flagSocket = cli.StringFlag{
		Name:  "socket, s",
		Value: "/usr/share/docker/plugins/ovs.sock",

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
		flagBridgeName,
		flagBridgeIP,
		flagBridgeSubnet,
	}
	app.Action = Run
	app.Before = cliInit
	app.Run(os.Args)
}

func cliInit(ctx *cli.Context) error {
	socketFile := ctx.String("socket")
	// Default loglevel is Info
	if ctx.Bool("debug") {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetLevel(log.InfoLevel)
	}
	log.SetOutput(os.Stderr)
	// Verify the path to the plugin socket exists
	// Should we make it if it doesnt exist?
	sockDir, _ := filepath.Split(socketFile)
	dhandle, err := os.Stat(sockDir)
	if err != nil {
		log.Fatalf("socket filepath [ %s ] does not exist", sockDir)
	}
	// Verify the path is a directory
	if !dhandle.IsDir() {
		log.Fatalf("socket filepath [ %s ] is not a directory", sockDir)
	}
	// If the ovs plugin socket file already exists, remove it.
	if _, err := os.Stat(socketFile); err == nil {
		log.Debugf("socket file [ %s ] already exists, attempting to remove it.", socketFile)
		removeSock(socketFile)
	}
	return nil
}

func Run(ctx *cli.Context) {
	// Replaced InitDefaultLogging with logrus
	//  but not sure on logging w/docker+plugin
	// InitDefaultLogging(false)
	var d Driver
	var err error
	if d, err = New(version); err != nil {
		Error.Fatalf("unable to create driver: %s", err)
	}
	if err := d.Listen(ctx.String("socket")); err != nil {
		Error.Fatal(err)
	}
}

func removeSock(sockFile string) {
	err := os.Remove(sockFile)
	if err != nil {
		log.Fatalf("unable to remove old socket file [ %s ] due to: %s", sockFile, err)
	}
}
