package main

func main() {
	InitDefaultLogging(false)
	var d driver.Driver
	if d, err := driver.New(version); err != nil {
		Error.Fatalf("unable to create driver: %s", err)
	}

	if err := d.Listen("/usr/share/docker/plugins/ovs.sock"); err != nil {
		Error.Fatal(err)
	}
}
