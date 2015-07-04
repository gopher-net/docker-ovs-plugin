docker-ovs-plugin
=================

Still a WIP, lots to do...

- [ ] network creation code
- [ ] endpoint creation code
- [ ] endpoint deletion code
- [ ] endpoint veth pair creation
- [ ] ovs flowmods for packet forwarding
- [x] bridge creation
- [x] ovsdb manager initializaiton
- [x] ovsdb table caching
- [x] add cli for the daemon
- [x] dockerfile to run the daemon using official golang image
- [x] compose file to run the openvswitch + daemon
- [ ] test it works!
- [ ] code cleanup - still lots of unused code
- [ ] readme with how-to and hat tip to weave (this was based on their plugin)


### Development


### Dev notes to run the OVS plugin directly on the Docker host OS

To build, test, dev the OVS plugin directly on the Docker host OS do the following  (requires Go to be setup).

```
$ apt-get install openvswitch-switch
# Tell OVS to listen for incoming OVSDB manager connections on port 6640
$ ovs-vsctl set-manager ptcp:6640

# Install Docker
$ wget -qO- https://experimental.docker.com/ | sh

# Clone and start the plugin
$ git clone https://github.com/dave-tucker/docker-ovs-plugin.git
$ cd docker-ovs-plugin
$ go run *.go

# Start the Docker daemon with the name of the plugin socket.
# In this case it is ovs  (/usr/share/docker/plugins/ovs.sock) 
# To run docker in the foreground for debugging first stop the service
$ systemctl stop docker (or depending on OS ver) /etc/init.d/docker stop 

# Start the service with the following options or add them to DOCKER_OPTS
# in /etc/default/docker Start the daemon with the debug flag
$ docker -d -D --default-network=ovs:foo

# Run a container
$ docker run -it --rm busybox

# Use ovs-vsctl to see the bridge and ports created
$ ovs-vsctl show

# Use ovsdb-client to view the OVSDB database
$ ovsdb-client dump
```

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


