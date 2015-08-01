docker-ovs-plugin
=================

### QuickStart Instructions

The quickstart instructions describe how to start the plugin in **nat mode**. Flat mode is described in the `flat` mode section.

1. Install the Docker experimental binary from the instructions at: [Docker Experimental](https://github.com/docker/docker/tree/master/experimental). (stop other docker instances)
	- Quick Experimental Install: `wget -qO- https://experimental.docker.com/ | sh`
1. Install and start Open vSwitch.

	- *Using apt-get*

	```
	$ sudo apt-get install openvswitch-switch 
	$ sudo /etc/init.d/openvswitch-switch start
	```

	- *Using yum*

	```
	$ sudo yum install openvswitch
	$ sudo /sbin/service openvswitch start
	```
2. Add OVSDB manager listener:

	```
	$ sudo ovs-vsctl set-manager ptcp:6640
	```

3. Start Docker with the following:
	
	```
	$sudo docker -d --default-network=ovs:ovsbr-docker0`
	```
 
 	Or edit the default configuration (e.g `/etc/default/docker`) and restart the service
 	```
 	$ sudo su
 	# echo 'DOCKER_OPTS="--default-network=ovs:ovsbr-docker0"' >> /etc/default/docker
 	# service docker restart
 	```
	
4. Next start the plugin. A pre-compiled x86_64 binary can be downloaded from the [binaries](https://github.com/gopher-net/docker-ovs-plugin/tree/master/binaries) directory. **Note:** Running inside a container is a todo, pop it into issues if you want to help contribute that.

	```
	$ wget -O ./docker-ovs-plugin https://github.com/gopher-net/docker-ovs-plugin/raw/master/binaries/docker-ovs-plugin-0.1-Linux-x86_64
	$ chmod +x docker-ovs-plugin
	$ ./docker-ovs-plugin
	```

	Running the binary with no options is the same as running the following. Any of those fields can be customized, just make sure your gateway is on the same network/subnet as the specified bridge subnet.

	```
	$ ./docker-ovs-plugin   --gateway=172.18.40.1  --bridge-subnet=172.18.40.0/24  -mode=nat
	```

	If you pass a subnet but not a gateway, we currently make an assumption that the first usable address. For example, in the case of a /24 subnet the .1 on the network will be used)

	For debugging or just extra logs from the sausage factory, add the debug flag `./docker-ovs-plugin -d`

5. Run some containers and verify they can ping one another with `docker run -it --rm busybox` or `docker run -it --rm ubuntu` etc, or any other docker images you prefer. Alternatively, paste a few dozen or more containers running in the background and watch the ports provision and de-provision in OVS with `docker run -itd busybox`

	```
	INFO[0000] Plugin configuration:
      container subnet: [172.18.40.0/24]
        container gateway: [172.18.40.1]
        bridge name: [ovsbr-docker0]
        bridge mode: [nat]
        mtu: [1450]
	INFO[0000] OVS network driver initialized successfully
	INFO[0005] Dynamically allocated container IP is: [ 172.18.40.2 ]
	INFO[0005] Attached veth [ ovs-veth0-ac097 ] to bridge [ ovsbr-docker0 ]
	INFO[0009] Deleted OVS port [ ovs-veth0-ac097 ] from bridge [ ovsbr-docker0 ]
	```

### Flat Mode

There are two generic modes, `flat` and `nat`. The default mode is `nat` since it does not require any orchestration with the network because the address space is hidden behind iptables masquerading.


- flat is simply an OVS bridge with the container link attached to it. An example would be a Docker host is plugged into a data center port that has a subnet of `192.168.1.0/24`. You would start the plugin like so:

```
$ docker-ovs-plugin --gateway=192.168.1.1 --bridge-subnet=192.168.1.0/24 -mode=flat
```

- Containers now start attached to an OVS bridge. It could be tagged or untagged but either way it is isolated and unable to communicate to anything outside of its bridge domain. In this case, you either add VXLAN tunnels to other bridges of the same bridge domain or add an `eth` interface to the bridge to allow access to the underlying network when traffic leaves the Docker host. To do so, you simply add the `eth` interface to the ovs bridge. Neither the bridge nor the eth interface need to have an IP address since traffic from the container is strictly L2. **Warning** if you are remoted into the physical host make sure you are not using an ethernet interface to attach to the bridge that is also your management interface since the eth interface no longer uses the IP address it had. The IP would need to be migrated to ovsbr-docker0 in this case. Allowing underlying network access to an OVS bridge can be done like so:

```
ovs-vsctl add-port ovsbr-docker0 eth2

```

Add an address to ovsbr-docker0 if you want an L3 interface on the L2 domain for the Docker host if you would like one for troubleshooting etc but it isn't required since flat mode cares only about MAC addresses and VLAN IDs like any other L2 domain would.

- Example of OVS with an ethernet interface bound to it for external access to the container sitting on the same bridge. NAT mode doesn't need the eth interface because IPTables is doing NAT/PAAT instead of bridging all the way through.


```
$ ovs-vsctl show
e0de2079-66f0-4279-a1c8-46ba0672426e
    Manager "ptcp:6640"
        is_connected: true
    Bridge "ovsbr-docker0"
        Port "ovsbr-docker0"
            Interface "ovsbr-docker0"
                type: internal
        Port "ovs-veth0-d33a9"
            Interface "ovs-veth0-d33a9"
        Port "eth2"
            Interface "eth2"
    ovs_version: "2.3.1"
```


### Additional Notes:

 - The argument passed to `--default-network` the plugin is identified via `ovs`. More specifically, the socket file that currently defaults to `/run/docker/plugins/ovs.sock`.
 - The default bridge name in the example is `ovsbr-docker0`.
 - The bridge name is temporarily hardcoded. That and more will be configurable via flags. (Help us define and code those flags).
 - Add other flags as desired such as `--dns=8.8.8.8` for DNS etc.
 - To view the Open vSwitch configuration, use `ovs-vsctl show`.
 - To view the OVSDB tables, run `ovsdb-client dump`. All of the mentioned OVS utils are part of the standard binary installations with very well documented [man pages](http://openvswitch.org/support/dist-docs/).
 - The containers are brought up on a flat bridge. This means there is no NATing occurring. A layer 2 adjacency such as a VLAN or overlay tunnel is required for multi-host communications. If the traffic needs to be routed an external process to act as a gateway (on the TODO list so dig in if interested in multi-host or overlays).
 - Download a quick video demo [here](https://dl.dropboxusercontent.com/u/51927367/Docker-OVS-Plugin.mp4).

### Hacking and Contributing

Yes!! Please see issues for todos or add todos into [issues](https://github.com/gopher-net/docker-ovs-plugin/issues)! Only rule here is no jerks.

Since this plugin uses netlink for L3 IP assignments, a Linux host that can build [vishvananda/netlink](https://github.com/vishvananda/netlink) library is required.

1. Install [Go](https://golang.org/doc/install). OVS as listed above and a kernel >= 3.19.

2. Clone and start the OVS plugin:

    ```
    git clone https://github.com/gopher-net/docker-ovs-plugin.git
    cd docker-ovs-plugin/plugin
    # Get the Go dependencies
    go get ./...
    go run main.go
    # or using explicit configuration flags:
    go run main.go -d --gateway=172.18.40.1 --bridge-subnet=172.18.40.0/24 -mode=nat
    ```

3. The rest is the same as the Quickstart Section.

 **Note:** If you are new to Go.

 - Go compile times are very fast due to linking being done statically. In order to link the libraries, Go looks for source code in the `~/go/src/` directory.
 - Typically you would clone the project to a directory like so `go/src/github.com/gopher-net/docker-ovs-plugin/`. Go knows where to look for the root of the go code, binaries and pkgs based on the `$GOPATH` shell ENV.
 - For example, you would clone to the path `/home/<username>/go/src/github.com/gopher-net/docker-ovs-plugin/` and put `export GOPATH=/home/<username>/go` in wherever you store your persistent ENVs in places like `~/.bashrc`, `~/.profile` or `~/.bash_profile` depending on the OS and system configuration.

### Thanks

Thanks to the guys at [Weave](http://weave.works) for writing their awesome [plugin](https://github.com/weaveworks/docker-plugin). We borrowed a lot of code from here to make this happen!
