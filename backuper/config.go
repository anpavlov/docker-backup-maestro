package backuper

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"os"
	"slices"
	"strings"

	"crypto/md5"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"gopkg.in/yaml.v2"
)

type Template struct {
	Image       string
	Entrypoint  []string
	Command     []string
	EnvFile     string            `yaml:"env_file"` // TODO:  parse also as slice (as interface)
	Environment map[string]string // TODO: parse also as slice
	Volumes     []string
	Labels      map[string]string
	Networks    []string
}

func (bCfg *Template) Hash() string {
	hash := md5.New()

	hash.Write([]byte(bCfg.Image))

	for _, elem := range bCfg.Entrypoint {
		hash.Write([]byte(elem))
	}

	for _, elem := range bCfg.Command {
		hash.Write([]byte(elem))
	}

	hash.Write([]byte(bCfg.EnvFile))

	for k, v := range bCfg.Environment {
		hash.Write([]byte(k))
		hash.Write([]byte(v))
	}

	for _, elem := range bCfg.Volumes {
		hash.Write([]byte(elem))
	}

	for k, v := range bCfg.Labels {
		hash.Write([]byte(k))
		hash.Write([]byte(v))
	}

	for _, elem := range bCfg.Networks {
		hash.Write([]byte(elem))
	}

	return hex.EncodeToString(hash.Sum(nil))
}

func (bCfg *Template) Overlay(other *Template) {
	if len(other.Image) != 0 {
		bCfg.Image = other.Image
	}

	if len(other.Entrypoint) != 0 {
		bCfg.Entrypoint = other.Entrypoint
	}

	if len(other.Command) != 0 {
		bCfg.Command = other.Command
	}

	for k, v := range other.Environment {
		bCfg.Environment[k] = v
	}

	if len(other.EnvFile) != 0 {
		bCfg.EnvFile = other.EnvFile
	}

	for _, v := range other.Volumes {
		if !slices.Contains(bCfg.Volumes, v) {
			bCfg.Volumes = append(bCfg.Volumes, v)
		}
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

func (bCfg *Template) CreateConfig() (*container.Config, *container.HostConfig, *network.NetworkingConfig, error) {
	if len(bCfg.EnvFile) != 0 {
		f, err := os.Open(bCfg.EnvFile)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("env_file '%s' open error: %w", bCfg.EnvFile, err)
		}

		defer f.Close()

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			envLine := scanner.Text()
			envKV := strings.SplitN(envLine, "=", 1)
			if len(envKV) == 1 {
				bCfg.Environment[envKV[0]] = ""
			} else {
				bCfg.Environment[envKV[0]] = envKV[1]
			}
		}

		if scanner.Err() != nil {
			return nil, nil, nil, fmt.Errorf("env_file '%s' read error: %w", bCfg.EnvFile, scanner.Err())
		}
	}

	envArr := make([]string, 0, len(bCfg.Environment))
	for envName, envVal := range bCfg.Environment {
		envArr = append(envArr, fmt.Sprintf("%s=%s", envName, envVal))
	}

	cntrCfg := &container.Config{
		Image:  bCfg.Image,
		Env:    envArr,
		Labels: bCfg.Labels,
	}

	if len(bCfg.Entrypoint) != 0 {
		cntrCfg.Entrypoint = bCfg.Entrypoint
	}

	if len(bCfg.Command) != 0 {
		cntrCfg.Cmd = bCfg.Command
	}

	hostCfg := &container.HostConfig{
		Binds: bCfg.Volumes,
	}

	var netCfg *network.NetworkingConfig

	if len(bCfg.Networks) != 0 {
		netCfg = &network.NetworkingConfig{}

		for _, netName := range bCfg.Networks {
			netCfg.EndpointsConfig[netName] = nil
		}
	}

	return cntrCfg, hostCfg, netCfg, nil
}

func ReadTemplateFromFile(path string) (*Template, error) {
	tmplData, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("backuper template '%s' read failed: %w", path, err)
	}

	expandedTmpl := os.ExpandEnv(string(tmplData))

	tmpl := &Template{}

	err = yaml.Unmarshal([]byte(expandedTmpl), tmpl)
	if err != nil {
		return nil, fmt.Errorf("backuper template '%s' parsing failed: %w", path, err)
	}

	return tmpl, nil
}
