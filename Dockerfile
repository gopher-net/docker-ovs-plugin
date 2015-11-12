FROM golang
COPY . /go/src/github.com/gopher-net/docker-ovs-plugin
WORKDIR /go/src/github.com/gopher-net/docker-ovs-plugin
RUN apt-get update && apt-get -y install iptables dbus
RUN go get github.com/tools/godep
RUN godep go install -v
ENTRYPOINT ["docker-ovs-plugin"]
