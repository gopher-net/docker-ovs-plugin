docker-ovs-plugin
=================

### QuickStart Instructions

The quickstart instructions describe how to start the plugin in **nat mode**. Flat mode is described in the `flat` mode section.

**1.** Make sure you are using Docker 1.9 or later

**2.** You need to `modprobe openvswitch` on the machine where the Docker Daemon is located

```
$ docker-machine ssh default "sudo modprobe openvswitch"
```

**3.** Create the following `docker-compose.yml` file

```yaml
plugin:
  image: gophernet/ovs-plugin
  volumes:
    - /run/docker/plugins:/run/docker/plugins
    - /var/run/docker.sock:/var/run/docker.sock
  net: host
  stdin_open: true
  tty: true
  privileged: true
  command: -d

ovs:
  image: socketplane/openvswitch:2.3.2
  cap_add:
    - NET_ADMIN
  net: host
```

**4.** `docker-compose up -d`

**5.** Now you are ready to create a new network

```
$ docker network create -d ovs mynet
```

**6.** Test it out!

```
$ docker run -itd --net=mynet --name=web nginx

$ docker run -it --rm --net=mynet busybox wget -qO- http://web
```

### Flat Mode

There are two generic modes, `flat` and `nat`. The default mode is `nat` since it does not require any orchestration with the network because the address space is hidden behind iptables masquerading.


- flat is simply an OVS bridge with the container link attached to it. An example would be a Docker host is plugged into a data center port that has a subnet of `192.168.1.0/24`. You would start the plugin like so:

```
$ docker-ovs-plugin --gateway=192.168.1.1 --bridge-subnet=192.168.1.0/24 -mode=flat
```

You can also add these flags to the `command` section of your `docker-compose.yml`

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

**Flat Mode Note:** Hosts will only be able to ping one another unless you add an ethernet interface to the `docker-ovsbr0` bridge with something like `ovs-vsctl add-port <bridge_name> <port_name>`. NAT mode will masquerade around that issue. It is an inherent hastle of bridges that is unavoidable. This is a reason bridgeless implementation [gopher-net/ipvlan-docker-plugin](https://github.com/gopher-net/ipvlan-docker-plugin) and [gopher-net/macvlan-docker-plugin](https://github.com/gopher-net/macvlan-docker-plugin) can be attractive.

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

2. Install [godeps](https://github.com/tools/godep) by running `go get github.com/tools/godep`.

3. Clone and start the OVS plugin:

    ```
    git clone https://github.com/gopher-net/docker-ovs-plugin.git
    cd docker-ovs-plugin/plugin
    # using godep restore will pull down the appropriate go dependencies
    godep restore
    go run main.go
    # or using explicit configuration flags:
    go run main.go -d --gateway=172.18.40.1 --bridge-subnet=172.18.40.0/24 -mode=nat
    ```

3. The rest is the same as the Quickstart Section.

 **Note:** If you are new to Go.

 - Go compile times are very fast due to linking being done statically. In order to link the libraries, Go looks for source code in the `~/go/src/` directory.
 - Typically you would clone the project to a directory like so `go/src/github.com/gopher-net/docker-ovs-plugin/`. Go knows where to look for the root of the go code, binaries and pkgs based on the `$GOPATH` shell ENV.
 - For example, you would clone to the path `/home/<username>/go/src/github.com/gopher-net/docker-ovs-plugin/` and put `export GOPATH=/home/<username>/go` in wherever you store your persistent ENVs in places like `~/.bashrc`, `~/.profile` or `~/.bash_profile` depending on the OS and system configuration.


#### Trying it out

If you want to try out some of your changes with your local docker install

- `docker-compose -f dev.yml up -d`

This will start Open vSwitch and the plugin running inside a container!

### Thanks

Thanks to the guys at [Weave](http://weave.works) for writing their awesome [plugin](https://github.com/weaveworks/docker-plugin). We borrowed a lot of code from here to make this happen!
