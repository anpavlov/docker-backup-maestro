package internal

import (
	"os"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/stretchr/testify/require"
)

func TestTemplateCreate(t *testing.T) {
	tmpl := Template{
		Image:       "example",
		Entrypoint:  []string{"entry"},
		Restart:     "unless-stopped",
		Command:     []string{},
		Volumes:     []string{"/data:/inside"},
		Networks:    []string{"example_net"},
		Labels:      map[string]string{"lbl": "txt", "lbl2": ""},
		Environment: map[string]string{"ENV1": "VAL1"},
		Devices:     []string{"/dev/sda:/dev/sdb"},
		Privileged:  true,
	}

	buildInfo, cntrCfg, hostCfg, netCfg, err := tmpl.CreateConfig("not used")
	require.NoError(t, err)

	require.Nil(t, buildInfo)

	require.Equal(t, *cntrCfg, container.Config{
		Image:      "example",
		Env:        []string{"ENV1=VAL1"},
		Labels:     map[string]string{"lbl": "txt", "lbl2": ""},
		Entrypoint: []string{"entry"},
		Cmd:        []string{},
	})

	require.Equal(t, *hostCfg, container.HostConfig{
		Binds: []string{"/data:/inside"},
		RestartPolicy: container.RestartPolicy{
			Name: container.RestartPolicyUnlessStopped,
		},
		Resources: container.Resources{Devices: []container.DeviceMapping{
			{
				PathOnHost:      "/dev/sda",
				PathInContainer: "/dev/sdb",
			},
		}},
		Privileged: true,
	})

	require.Equal(t, *netCfg, network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{"example_net": nil},
	})

}

func TestTemplateCreateEnvFile(t *testing.T) {
	f, err := os.CreateTemp("", "env_file")
	require.NoError(t, err)

	defer f.Close()
	defer os.Remove(f.Name())

	f.WriteString("ENV2=VAL2")

	tmpl := Template{
		EnvFile: []string{f.Name()},
	}

	_, cntrCfg, _, _, err := tmpl.CreateConfig("not used")
	require.NoError(t, err)

	require.Equal(t, *cntrCfg, container.Config{
		Env: []string{"ENV2=VAL2"},
	})

}

func TestTemplateOverlay(t *testing.T) {
	tmpl1 := Template{
		Image:       "example",
		Entrypoint:  []string{"entry"},
		EnvFile:     []string{"env_file"},
		Restart:     "unless-stopped",
		Command:     []string{"cmd"},
		Volumes:     []string{"/data1:/inside1"},
		Networks:    []string{"net"},
		Labels:      map[string]string{"lbl": "txt", "lbl2": ""},
		Environment: map[string]string{"ENV1": "VAL1"},
	}

	tmpl2 := Template{
		Image:       "bar",
		Entrypoint:  []string{"entry2"},
		EnvFile:     []string{"env_file2"},
		Restart:     "always",
		Command:     []string{"cmd2"},
		Volumes:     []string{"/data2:/inside2"},
		Networks:    []string{"net2"},
		Labels:      map[string]string{"lbl": "boo", "lbl3": "hello"},
		Environment: map[string]string{"ENV1": "VAL!", "ENV2": "VAL2"},
	}

	tmpl_res := tmpl1.Overlay(&tmpl2)

	require.Equal(t, tmpl_res.Image, "bar")
	require.Equal(t, tmpl_res.Entrypoint, ShellCommand([]string{"entry2"}))
	require.Equal(t, tmpl_res.EnvFile, StringOneOrArray([]string{"env_file", "env_file2"}))
	require.Equal(t, tmpl_res.Restart, "always")
	require.Equal(t, tmpl_res.Command, ShellCommand([]string{"cmd2"}))
	require.Equal(t, tmpl_res.Volumes, []string{"/data1:/inside1", "/data2:/inside2"})
	require.Equal(t, tmpl_res.Networks, []string{"net", "net2"})
	require.Equal(t, tmpl_res.Labels, StringMapOrArray(map[string]string{"lbl": "boo", "lbl2": "", "lbl3": "hello"}))
	require.Equal(t, tmpl_res.Environment, StringMapOrArray(map[string]string{"ENV1": "VAL!", "ENV2": "VAL2"}))
}

func TestTemplateOverlayBuild(t *testing.T) {
	buildInfo := BuildInfo{
		Context: ".",
	}

	tmpl1 := Template{
		Image: "img",
		Build: buildInfo,
	}

	tmpl2 := Template{
		Image: "alpine",
	}

	tmpl_res := tmpl1.Overlay(&tmpl2)

	require.Equal(t, tmpl_res.Build, BuildInfo{})
	require.Equal(t, tmpl_res.Image, "alpine")

	tmpl1 = Template{
		Image: "alpine",
	}

	tmpl2 = Template{
		Image: "img",
		Build: buildInfo,
	}

	tmpl_res = tmpl1.Overlay(&tmpl2)

	require.Equal(t, tmpl_res.Build, buildInfo)
	require.Equal(t, tmpl_res.Image, "img")

	tmpl1 = Template{
		Image: "alpine",
	}

	tmpl2 = Template{
		Build: buildInfo,
	}

	tmpl_res = tmpl1.Overlay(&tmpl2)

	require.Equal(t, tmpl_res.Build, buildInfo)
	require.Equal(t, tmpl_res.Image, "")
}

func TestTemplateParse(t *testing.T) {
	f, err := os.CreateTemp("", "test_tmpl")
	require.NoError(t, err)

	defer f.Close()
	defer os.Remove(f.Name())

	os.Setenv("VAR", "varval")
	os.Setenv("VAR2", "var2val")

	tmplStr1 := `image: alpine
build: .
entrypoint: hello
command: cmd subcmd exec "-p 800 ${VAR}"
restart: unless-stopped
env_file: .env
environment:
  - ENV=${VAR2}
  - ENV1=VAL
volumes:
  - /host:/cntr
labels:
  - lbl1=val1
  - lbl2=val2
networks:
  - net1
`

	f.WriteString(tmplStr1)

	tmpl, err := ReadTemplateFromFile(f.Name(), true)
	require.NoError(t, err)

	require.Equal(t, tmpl.Image, "alpine")
	require.Equal(t, tmpl.Build, BuildInfo{
		Context: ".",
	})
	require.Equal(t, tmpl.Entrypoint, ShellCommand([]string{"hello"}))
	require.Equal(t, tmpl.Command, ShellCommand([]string{"cmd", "subcmd", "exec", "-p 800 varval"}))
	require.Equal(t, tmpl.Restart, "unless-stopped")
	require.Equal(t, tmpl.EnvFile, StringOneOrArray([]string{".env"}))
	require.Equal(t, tmpl.Environment, StringMapOrArray(map[string]string{"ENV": "var2val", "ENV1": "VAL"}))
	require.Equal(t, tmpl.Volumes, []string{"/host:/cntr"})
	require.Equal(t, tmpl.Labels, StringMapOrArray(map[string]string{"lbl1": "val1", "lbl2": "val2"}))
	require.Equal(t, tmpl.Networks, []string{"net1"})

	f.Truncate(0)
	f.Seek(0, 0)

	tmplStr2 := `image: alpine
build:
  context: /ctx
  dockerfile: cfg/Dockerfile
env_file:
  - .env2
environment:
  ENV: ${VAR2}
  ENV1: VAL
`

	f.WriteString(tmplStr2)

	tmpl, err = ReadTemplateFromFile(f.Name(), true)
	require.NoError(t, err)

	require.Equal(t, tmpl.Image, "alpine")
	require.Equal(t, tmpl.Build, BuildInfo{
		Context:    "/ctx",
		Dockerfile: "cfg/Dockerfile",
	})
	require.Equal(t, tmpl.EnvFile, StringOneOrArray([]string{".env2"}))
	require.Equal(t, tmpl.Environment, StringMapOrArray(map[string]string{"ENV": "var2val", "ENV1": "VAL"}))
}
