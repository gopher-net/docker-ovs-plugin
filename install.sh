#!/bin/sh

touch /run/docker/plugins/ovs.sock
docker-compose up -d
