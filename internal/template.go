package internal

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"strconv"
	"strings"

	"crypto/md5"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/tiendc/go-deepcopy"
	"gopkg.in/yaml.v2"
)

type Template struct {
	Image       string
	Entrypoint  []string
	Command     []string
	Restart     string
	EnvFile     string            `yaml:"env_file"` // TODO:  parse also as slice (as interface)
	Environment map[string]string // TODO: parse also as slice
	Volumes     []string
	Labels      map[string]string
	Networks    []string
}

func (bCfg *Template) Hash() string {
	hashMd5 := md5.New()

	jsonStr, err := json.Marshal(bCfg)
	if err != nil {
		log.Fatalln(err)
	}

	hashMd5.Write(jsonStr)

	hashHex := hex.EncodeToString(hashMd5.Sum(nil))

	return hashHex
}

func (bCfg *Template) Overlay(other *Template) *Template {
	newTmpl := Template{}

	err := deepcopy.Copy(&newTmpl, bCfg)
	if err != nil {
		log.Fatal("deepcopy failed:", err)
	}

	if len(other.Image) != 0 {
		newTmpl.Image = other.Image
	}

	if len(other.Entrypoint) != 0 {
		newTmpl.Entrypoint = other.Entrypoint
	}

	if len(other.Command) != 0 {
		newTmpl.Command = other.Command
	}

	for k, v := range other.Environment {
		newTmpl.Environment[k] = v
	}

	if len(other.Restart) != 0 {
		newTmpl.Restart = other.Restart
	}

	if len(other.EnvFile) != 0 {
		newTmpl.EnvFile = other.EnvFile
	}

	for _, v := range other.Volumes {
		if !slices.Contains(newTmpl.Volumes, v) {
			newTmpl.Volumes = append(newTmpl.Volumes, v)
		}
	}

	for k, v := range other.Labels {
		newTmpl.Labels[k] = v
	}

	for _, k := range other.Networks {
		if !slices.Contains(newTmpl.Networks, k) {
			newTmpl.Networks = append(newTmpl.Networks, k)
		}
	}

	return &newTmpl
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

	rst, err := parseRestart(bCfg.Restart)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to parse restart '%s' - %w", bCfg.Restart, err)
	}

	hostCfg := &container.HostConfig{
		Binds:         bCfg.Volumes,
		RestartPolicy: rst,
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

func ReadTemplateFromFile(path string, required bool) (*Template, error) {
	tmplData, err := os.ReadFile(path)
	if err != nil && errors.Is(err, os.ErrNotExist) && !required {
		return nil, nil
	}

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

func parseRestart(restart string) (pol container.RestartPolicy, err error) {
	parts := strings.Split(restart, ":")
	switch {
	case len(parts) > 2:
		err = fmt.Errorf("restart format invalid. more than one column found '%s'", restart)
		return

	case len(parts) == 2:
		var retries int
		retries, err = strconv.Atoi(parts[1])
		if err != nil {
			err = fmt.Errorf("failed to parse retries number as number '%s' - %w", parts[1], err)
			return
		}

		pol.Name = container.RestartPolicyMode(parts[0])
		pol.MaximumRetryCount = retries
	case len(parts) == 1:
		pol.Name = container.RestartPolicyMode(parts[0])
	}

	err = container.ValidateRestartPolicy(pol)
	return
}
