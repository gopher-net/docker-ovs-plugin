package plugin

import "github.com/samalba/dockerclient"

type dockerer struct {
	client *dockerclient.DockerClient
}
