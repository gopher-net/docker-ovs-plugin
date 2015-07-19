package main

import (
	"os"
	"path/filepath"

	log "github.com/Sirupsen/logrus"
	"github.com/codegangsta/cli"
	"github.com/gopher-net/docker-ovs-plugin/plugin/ovs"
)

const version = "0.1"

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
		ovs.FlagBridgeName,
		ovs.FlagBridgeIP,
		ovs.FlagBridgeSubnet,
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
	// Verify the path to the plugin socket oath and filename were passed
	sockDir, fileHandle := filepath.Split(socketFile)
	if fileHandle == "" {
		log.Fatalf("Socket file path and name are required. Ex. /usr/share/docker/plugins/<plugin_name>.sock")
	}
	// Make the plugin filepath and parent dir if it does not already exist
	if err := os.MkdirAll(sockDir, 0755); err != nil && !os.IsExist(err) {
		log.Warnf("Could not create net plugin path directory: [ %s ]", err)
	}
	// If the plugin socket file already exists, remove it.
	if _, err := os.Stat(socketFile); err == nil {
		log.Debugf("socket file [ %s ] already exists, deleting..", socketFile)
		removeSock(socketFile)
	}
	log.Debugf("Plugin socket path is [ %s ] with a file handle [ %s ]", sockDir, fileHandle)
	return nil
}

// Run initializes the driver
func Run(ctx *cli.Context) {
	var d ovs.Driver
	var err error
	if d, err = ovs.New(version); err != nil {
		log.Fatalf("unable to create driver: %s", err)
	}
	log.Info("OVSDB network driver initialized")
	if err := d.Listen(ctx.String("socket")); err != nil {
		log.Fatal(err)
	}
}

func removeSock(sockFile string) {
	err := os.Remove(sockFile)
	if err != nil {
		log.Fatalf("unable to remove old socket file [ %s ] due to: %s", sockFile, err)
	}
}
