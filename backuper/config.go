package backuper

import (
	"fmt"
	"slices"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
)

type Config struct {
	Image      string
	Entrypoint string
	Command    string
	Env        map[string]string
	Binds      map[string]string
	Labels     map[string]string
	Networks   []string
}

func (bCfg *Config) Overlay(other *Config) {
	if len(other.Image) != 0 {
		bCfg.Image = other.Image
	}

	if len(other.Entrypoint) != 0 {
		bCfg.Entrypoint = other.Entrypoint
	}

	if len(other.Command) != 0 {
		bCfg.Command = other.Command
	}

	for k, v := range other.Env {
		bCfg.Env[k] = v
	}

	for k, v := range other.Binds {
		bCfg.Binds[k] = v
	}

	for k, v := range other.Labels {
		bCfg.Labels[k] = v
	}

	for _, k := range other.Networks {
		if !slices.Contains(bCfg.Networks, k) {
			bCfg.Networks = append(bCfg.Networks, k)
		}
	}
}

func (bCfg *Config) CreateConfig() (config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig) {
	envArr := make([]string, 0, len(bCfg.Env))
	for envName, envVal := range bCfg.Env {
		envArr = append(envArr, fmt.Sprintf("%s=%s", envName, envVal))
	}

	cntrCfg := &container.Config{
		Image:  bCfg.Image,
		Env:    envArr,
		Labels: bCfg.Labels,
	}

	if len(bCfg.Entrypoint) != 0 {
		cntrCfg.Entrypoint = strings.Split(bCfg.Entrypoint, " ")
	}

	if len(bCfg.Command) != 0 {
		cntrCfg.Cmd = strings.Split(bCfg.Command, " ")
	}

	bindsArr := make([]string, 0, len(bCfg.Binds))
	for bindSrc, bindDst := range bCfg.Binds {
		bindsArr = append(bindsArr, fmt.Sprintf("%s:%s", bindSrc, bindDst))
	}

	hostCfg := &container.HostConfig{
		Binds: bindsArr,
	}

	var netCfg *network.NetworkingConfig

	if len(bCfg.Networks) != 0 {
		netCfg = &network.NetworkingConfig{}

		for _, netName := range bCfg.Networks {
			netCfg.EndpointsConfig[netName] = nil
		}
	}

	return cntrCfg, hostCfg, netCfg
}
