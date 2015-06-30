package main

import (
	"os"

	"github.com/codegangsta/cli"
)

func main() {

	app := cli.NewApp()
	app.Name = "don"
	app.Usage = "Docker Open vSwitch Networking"
	app.Version = "0.0.1"
	app.Action = Run
	app.Run(os.Args)
}

func Run(ctx *cli.Context) {

	InitDefaultLogging(false)
	var d driver.Driver
	if d, err := driver.New(version); err != nil {
		Error.Fatalf("unable to create driver: %s", err)
	}

	if err := d.Listen("/usr/share/docker/plugins/ovs.sock"); err != nil {
		Error.Fatal(err)
	}

}
