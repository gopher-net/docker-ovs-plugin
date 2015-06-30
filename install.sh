#!/bin/sh

touch /usr/share/docker/plugins/ovs.sock
docker-compose up -d
