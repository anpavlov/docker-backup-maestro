package internal

import (
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/stretchr/testify/require"
)

func TestTemplateCreateWoBuild(t *testing.T) {

	tmpl := Template{
		Image:       "example",
		Entrypoint:  []string{"entry"},
		Restart:     "unless-stopped",
		Command:     []string{"cmd"},
		Volumes:     []string{"/data:/inside"},
		Networks:    []string{"example_net"},
		Labels:      map[string]string{"lbl": "txt", "lbl2": ""},
		Environment: map[string]string{"ENV1": "VAL1"},
	}

	buildInfo, cntrCfg, hostCfg, netCfg, err := tmpl.CreateConfig("not used")
	require.NoError(t, err)

	require.Nil(t, buildInfo)

	require.Equal(t, *cntrCfg, container.Config{
		Image:      "example",
		Env:        []string{"ENV1=VAL1"},
		Labels:     map[string]string{"lbl": "txt", "lbl2": ""},
		Entrypoint: []string{"entry"},
		Cmd:        []string{"cmd"},
	})

	require.Equal(t, *hostCfg, container.HostConfig{
		Binds: []string{"/data:/inside"},
		RestartPolicy: container.RestartPolicy{
			Name: container.RestartPolicyUnlessStopped,
		},
	})

	require.Equal(t, *netCfg, network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{"example_net": nil},
	})

}
