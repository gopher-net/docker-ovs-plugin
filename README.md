docker-ovs-plugin
=================

Still a WIP, lots to do...

- [x] network creation code
- [x] ip allocation via libnetwork ipam code
- [x] endpoint creation code
- [ ] endpoint deletion code
- [ ] determine CLI opts and binding of the values to Driver
- [x] gateway/default route code
- [x] need to deter
- [x] endpoint veth pair creation
- [x] ovs flowmods for packet forwarding. Currently OFPP_Normal
- [x] bridge creation
- [x] ovsdb manager initializaiton
- [x] ovsdb table caching
- [x] add cli for the daemon
- [x] dockerfile to run the daemon using official golang image
- [x] compose file to run the openvswitch + daemon
- [ ] test it works!
- [ ] code cleanup - still lots of unused code
- [ ] restart handling issues
- [ ] readme with how-to and hat tip to weave (this was based on their plugin)


### Caveats

* To test using the flag `--default-network` it requires docker experimental.
* OVS bridge name, bridge IP, container subnet are all hardcoded currently. We need to bind them to `Driver` via flag opts. Added a `brOpts` struct we could modify the `Driver` interface. Either way, lets figure out how we want to define and pass in bridge/IP properties. I think enabling the user to define individual container IP and/or subnet, bridge name, gatway etc. Eseentially everything pipeworks enables.
* Libnetwork currently appends an index to the container side iface. If OVS internal ports are renamed after bring moved to a namespace it breaks the port. We need to change this in Libnetwork. Something like a check for duplicates should satisfy the need for index appending.
* A default gateway/route now works but has to be passed as a destination route `0.0.0.0/0` rather then using the 'Gw' field in the route struct.

### Functional Status

After you do the above, you should see the following in the OVS config. 

```
$ ovs-vsctl show
ec580710-78ed-449c-83f2-4b708b6f7718
    Manager "ptcp:6640"
        is_connected: true
    Bridge "ovsbr-docker0"
        Port "ovsbr-docker0"
            Interface "ovsbr-docker0"
                type: internal
```

When you start a container, you will see the connected and default routes with the `route` command. 

**Note** the portname needs to be something friendlier then the nspid.

```
/ # ifconfig
c458212ce22c Link encap:Ethernet  HWaddr C2:E5:D4:CE:7A:23
          inet addr:172.18.40.2  Bcast:0.0.0.0  Mask:255.255.255.255
          UP BROADCAST RUNNING  MTU:1500  Metric:1
...        
docker run -it --rm busybox
/ # route
Kernel IP routing table
Destination     Gateway         Genmask         Flags Metric Ref    Use Iface
default         *               0.0.0.0         U     0      0        0 b621956803ea
172.18.40.0     *               255.255.255.0   U     0      0        0 b621956803ea
```
Once you have two containers started, they can ping one another. If they are on two seperate Docker hosts, for containers to communicate to one another they either need to be on the same LAN segment (VLAN / Layer2 connectivity) through the physical network or setup OVS VXLAN tunnels between the OVS instances on each docker hosts.

### Hacking and Contributing

* Instructions for building docker with the patch below applied to libnetwork in the docker build along with starting the plugin as follows:

1. `git clone https://github.com/docker/docker.git`
2. `cd docker`
3. `vi vendor/src/github.com/docker/libnetwork/sandbox/interface_linux.go` comment out the rename section in the patch below.
4. Build docker passing the the experimental flag. `DOCKER_EXPERIMENTAL=true make`. This is what provides `--default-network` flag to the docker daemon options. Note: A Docker daemon needs to be running when you run `make` in Docker since it builds the binary in a container.
5. Once the build is complete, you will have a docker binary with the libnetwork experimental flags supported. Start the docker damon with `./bundles/1.8.0-dev/binary/docker -d -D --default-network=ovs:ovsbr-docker0` (-D debug is optional). `ovsbr-docker0` is the OVS bridge name used.  
6. Clone the ovs plugin with `git clone https://github.com/dave-tucker/docker-ovs-plugin.git`. 
7. Start the plugin with `go run *.go -d`
8. Run a couple of containers and verify they can ping one another with `docker run -it --rm busybox` or any other docker runs you prefer.

If you want to download a pre-compiled binary a build from 7/8/15 can be found [here](https://www.dropbox.com/s/yzg9mttvw3ddtbc/docker-experimental-amd64-linux.tar?dl=1)

* Iterface renaming patch: Currently libnetwork renames the port within the container namespace. This breaks the OVS internal port. To run this driver, you can simply comment out the following in `vendor/src/github.com/docker/libnetwork/sandbox/interface_linux.go`


```
diff --git a/vendor/src/github.com/docker/libnetwork/sandbox/interface_linux.go
index 7fc8c70..b12e08e 100644
--- a/vendor/src/github.com/docker/libnetwork/sandbox/interface_linux.go
+++ b/vendor/src/github.com/docker/libnetwork/sandbox/interface_linux.go
@@ -141,15 +141,15 @@ func (i *nwIface) Remove() error {
                                return err
                        }
                }
-
-               n.Lock()
-               for index, intf := range n.iFaces {
-                       if intf == i {
-                               n.iFaces = append(n.iFaces[:index], n.iFaces[in
-                               break
-                       }
-               }
-               n.Unlock()

+               // n.Lock()
+               // for index, intf := range n.iFaces {
+               //      if intf == i {
+               //              n.iFaces = append(n.iFaces[:index], n.iFaces[in
+               //              break
+               //      }
+               // }
+               // n.Unlock()
                return nil
        })
```

* Additional setup and OVS specific commands:

```
$ apt-get install openvswitch-switch
# Tell OVS to listen for incoming OVSDB manager connections on port 6640
$ ovs-vsctl set-manager ptcp:6640

# Install Docker with the instructions above patching the interface rename and building the experimental binary.

# Clone and start the plugin
$ git clone https://github.com/dave-tucker/docker-ovs-plugin.git
$ cd docker-ovs-plugin
$ go run *.go -d

# Start the Docker daemon with the name of the plugin socket.
# In this case it is ovs  (/usr/share/docker/plugins/ovs.sock) 
# To run docker in the foreground for debugging first stop the service
$ systemctl stop docker (or depending on OS ver) /etc/init.d/docker stop 

# Start the service with the following options or add them to DOCKER_OPTS
# in /etc/default/docker Start the daemon with the debug flag
$ docker -d -D --default-network=ovs:ovsbr-docker0

# Run a container
$ docker run -it --rm busybox

# Use ovs-vsctl to see the bridge and ports created
$ ovs-vsctl show

# Use ovsdb-client to view the OVSDB database
$ ovsdb-client dump
```
