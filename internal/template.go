package internal

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"

	"crypto/md5"

	"github.com/compose-spec/compose-go/v2/dotenv"
	composegoutils "github.com/compose-spec/compose-go/v2/utils"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/mattn/go-shellwords"
	"github.com/tiendc/go-deepcopy"
	"gopkg.in/yaml.v2"
)

type ShellCommand []string

func (val *ShellCommand) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var a []string
	err := unmarshal(&a)
	if err != nil {
		var s string

		err := unmarshal(&s)
		if err != nil {
			return err
		}

		*val, err = shellwords.Parse(s)
		if err != nil {
			return err
		}
	} else {
		*val = a
	}
	return nil
}

type StringOneOrArray []string

func (val *StringOneOrArray) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var a []string
	err := unmarshal(&a)
	if err != nil {
		var s string
		err := unmarshal(&s)
		if err != nil {
			return err
		}
		*val = []string{s}
	} else {
		*val = a
	}
	return nil
}

type StringMapOrArray map[string]string

func (val *StringMapOrArray) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var m map[string]string
	err := unmarshal(&m)
	if err != nil {
		var a []string
		err := unmarshal(&a)
		if err != nil {
			return err
		}
		*val = composegoutils.GetAsEqualsMap(a)
	} else {
		*val = m
	}
	return nil
}

type BuildInfo struct {
	Data struct {
		Context    string
		Dockerfile string
	}
}

func (val *BuildInfo) UnmarshalYAML(unmarshal func(interface{}) error) error {
	err := unmarshal(&val.Data)
	if err != nil {
		var s string
		err := unmarshal(&s)
		if err != nil {
			return err
		}
		val.Data.Context = s
	}
	return nil
}

type Template struct {
	Build       BuildInfo
	Image       string
	Entrypoint  ShellCommand
	Command     ShellCommand
	Restart     string
	EnvFile     StringOneOrArray `yaml:"env_file"`
	Environment StringMapOrArray
	Volumes     []string
	Labels      StringMapOrArray
	Networks    []string

	autoRemove bool
}

func (tmpl *Template) Hash() string {
	hashMd5 := md5.New()

	jsonStr, err := json.Marshal(tmpl)
	if err != nil {
		log.Fatalln(err)
	}

	hashMd5.Write(jsonStr)

	hashHex := hex.EncodeToString(hashMd5.Sum(nil))

	return hashHex
}

func (tmpl *Template) Overlay(other *Template) *Template {
	newTmpl := Template{}

	err := deepcopy.Copy(&newTmpl, tmpl)
	if err != nil {
		log.Fatal("deepcopy failed:", err)
	}

	if len(other.Build.Data.Context) != 0 || len(other.Build.Data.Dockerfile) != 0 {
		newTmpl.Build = other.Build

		if len(other.Image) == 0 {
			newTmpl.Image = ""
		}
	}

	if len(other.Image) != 0 {
		newTmpl.Image = other.Image

		if len(other.Build.Data.Context) == 0 && len(other.Build.Data.Dockerfile) == 0 {
			newTmpl.Build = BuildInfo{}
		}
	}

	if len(other.Entrypoint) != 0 {
		newTmpl.Entrypoint = other.Entrypoint
	}

	if len(other.Command) != 0 {
		newTmpl.Command = other.Command
	}

	if newTmpl.Environment == nil {
		newTmpl.Environment = other.Environment
	} else {
		maps.Copy(newTmpl.Environment, other.Environment)
	}

	if len(other.Restart) != 0 {
		newTmpl.Restart = other.Restart
	}

	newTmpl.EnvFile = append(newTmpl.EnvFile, other.EnvFile...)
	slices.Sort(newTmpl.EnvFile)

	for _, v := range other.Volumes {
		if !slices.Contains(newTmpl.Volumes, v) {
			newTmpl.Volumes = append(newTmpl.Volumes, v)
		}
	}

	slices.Sort(newTmpl.Volumes)

	if newTmpl.Labels == nil {
		newTmpl.Labels = other.Labels
	} else {
		maps.Copy(newTmpl.Labels, other.Labels)
	}

	for _, k := range other.Networks {
		if !slices.Contains(newTmpl.Networks, k) {
			newTmpl.Networks = append(newTmpl.Networks, k)
		}
	}

	slices.Sort(newTmpl.Networks)

	return &newTmpl
}

func (tmpl *Template) CreateConfig(tag string) (*BuildInfo, *container.Config, *container.HostConfig, *network.NetworkingConfig, error) {
	var (
		environment map[string]string
	)

	if len(tmpl.EnvFile) != 0 {
		var err error
		environment, err = dotenv.GetEnvFromFile(composegoutils.GetAsEqualsMap(os.Environ()), tmpl.EnvFile)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("failed to read env file: %w", err)
		}
	}

	// FIXME we already expanded env vars when parsing yaml file. either we do not expand second time here, or we do not expand
	// on file parsing and do expand individualy every needed field (env, network, labels, etc..)
	if tmpl.Environment != nil {
		envMap, err := dotenv.ParseWithLookup(strings.NewReader(strings.Join(composegoutils.GetAsStringList(tmpl.Environment), "\n")), os.LookupEnv)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("")
		}

		if environment == nil {
			environment = envMap
		} else {
			maps.Copy(environment, envMap)
		}
	}

	var envArr []string
	if len(environment) > 0 {
		envArr = composegoutils.GetAsStringList(environment)
	}

	cntrCfg := &container.Config{
		Image:  tmpl.Image,
		Env:    envArr,
		Labels: tmpl.Labels,
	}

	if tmpl.Entrypoint != nil {
		cntrCfg.Entrypoint = []string(tmpl.Entrypoint)
	}

	if tmpl.Command != nil {
		cntrCfg.Cmd = []string(tmpl.Command)
	}

	rst, err := parseRestart(tmpl.Restart)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to parse restart '%s' - %w", tmpl.Restart, err)
	}

	hostCfg := &container.HostConfig{
		Binds:         tmpl.Volumes,
		RestartPolicy: rst,
		AutoRemove:    tmpl.autoRemove,
	}

	var netCfg *network.NetworkingConfig

	if len(tmpl.Networks) != 0 {
		netCfg = &network.NetworkingConfig{
			EndpointsConfig: make(map[string]*network.EndpointSettings),
		}

		for _, netName := range tmpl.Networks {
			netCfg.EndpointsConfig[netName] = nil
		}
	}

	var buildInfo *BuildInfo
	if len(tmpl.Build.Data.Context) > 0 || len(tmpl.Build.Data.Dockerfile) > 0 {
		buildInfo = &tmpl.Build

		if len(cntrCfg.Image) == 0 {
			cntrCfg.Image = tag
		}
	}

	return buildInfo, cntrCfg, hostCfg, netCfg, nil
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

	slices.Sort(tmpl.Volumes)
	slices.Sort(tmpl.EnvFile)
	slices.Sort(tmpl.Networks)

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
