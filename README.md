docker-ovs-plugin
=================

### Pre-Requisites

1. Install the Docker experimental binary from the instructions at: [Docker Experimental](https://github.com/docker/docker/tree/master/experimental). (stop other docker instances)
	- Quick Experimental Install: `wget -qO- https://experimental.docker.com/ | sh`

2. Install Open vSwitch.

	- *Using apt-get*

	```
	$ sudo apt-get install openvswitch-switch 
	$ /etc/init.d/openvswitch start
	```

	- *Using yum*

	```
	$ sudo yum install openvswitch
	$ sudo /sbin/service openvswitch start
	```

### QuickStart Instructions

1. Install and start Open vSwitch.
2. Add OVSDB manager listener `ovs-vsctl set-manager ptcp:6640`
3. Start Docker with the following.
`docker -d --default-network=ovs:ovsbr-docker0`

4. Next start the plugin. A pre-compiled x86_64 binary can be downloaded from the [binaries](https://github.com/dave-tucker/docker-ovs-plugin/binaries) directory. **Note:** Running inside a container is a todo, pop it into issues if you want to help contribute that. 

	```
	$ wget -O ./docker-ovs-plugin https://github.com/dave-tucker/docker-ovs-plugin/binaries/docker-ovs-plugin-0.1-Linux-x86_64
	$ chmod +x docker-ovs-plugin
	$ ./docker-ovs-plugin
	```

5. Start the plugin with `./docker-ovs-plugin` for debugging or just extra logs from the sausage factory, add the debug flag `./docker-ovs-plugin -d`

6. Run some containers and verify they can ping one another with `docker run -it --rm busybox` or `docker run -it --rm ubuntu` etc, any other docker images you prefer. Alternatively, paste a few dozen or more containers running in the background and watch the ports provision and de-provision in OVS with `docker run -itd busybox`

	```
	INFO[0000] OVSDB network driver initialized
	INFO[0005] Dynamically allocated container IP is: [ 172.18.40.2 ]
	INFO[0005] Attached veth [ ovs-veth0-ac097 ] to bridge [ ovsbr-docker0 ]
	INFO[0009] Deleted OVS port [ ovs-veth0-ac097 ] from bridge [ ovsbr-docker0 ]
	```

 **Additional Notes**: 
 - The argument passed to `--default-network` the plugin is identified via `ovs`. More specifically, the socket file that currently defaults to `/usr/share/docker/plugins/ovs.sock`.
 - The default bridge name in the example is `ovsbr-docker0`. 
 - The bridge name is temporarily hardcoded. That and more will be configurable via flags. (Help us define and code those flags). 
 - Add other flags as desired such as `--dns=8.8.8.8` for DNS etc.
 - To view the Open vSwitch configuration, use `ovs-vsctl show`.
 - To view the OVSDB tables, run `ovsdb-client dump`. All of the mentioned OVS utils are part of the standard binary installations with very well documented [man pages](http://openvswitch.org/support/dist-docs/). 
 - The containers are brought up on a flat bridge. This means there is no NATing occurring. A layer 2 adjacency such as a VLAN or overlay tunnel is required for multi-host communications. If the traffic needs to be routed an external process to act as a gateway (on the TODO list so dig in if interested in multi-host or overlays). 
 - Download a quick video demo [here](https://dl.dropboxusercontent.com/u/51927367/Docker-OVS-Plugin.mp4). 
 
### Hacking and Contributing

Yes!! Please see issues for todos or add todos into [issues](https://github.com/gopher-net/docker-ovs-plugin/issues)! Only rule here is no jerks.

### Thanks

Thanks to the guys at [Weave](http://weave.works) for writing their awesome [plugin](https://github.com/weaveworks/docker-plugin). We borrowed a lot of code from here to make this happen!
