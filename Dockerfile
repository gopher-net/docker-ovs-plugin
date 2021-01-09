FROM busybox
COPY docker-ovs-plugin /
WORKDIR /
ENTRYPOINT ["/docker-ovs-plugin"]
