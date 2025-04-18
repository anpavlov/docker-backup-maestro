package internal

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"

	controlapi "github.com/moby/buildkit/api/services/control"
	"google.golang.org/protobuf/proto"
)

const (
	ContainerStatusRunning    = "running"
	ContainerStatusRestarting = "restarting"
)

type buildRespLine struct {
	Message string
	Error   string
	Stream  string
	Aux     interface{}
}

type pullRespLine struct {
	Message  string
	Status   string
	Id       string
	Progress string
	Error    string
}

func (mngr *ContainerManager) listContainersWithLabel(ctx context.Context, label string, searchAll bool) ([]types.Container, error) {
	var opts container.ListOptions

	opts.Filters = filters.NewArgs()
	opts.Filters.Add("label", label)

	opts.All = searchAll

	return mngr.docker.ContainerList(ctx, opts)
}

func (mngr *ContainerManager) handleDockerEvent(ctx context.Context, event events.Message) error {
	if event.Action == events.ActionCreate {
		return mngr.createBackuper(ctx, event.Actor.Attributes[mngr.labels.backupName])
	} else if event.Action == events.ActionDestroy {
		return mngr.dropBackuper(ctx, event.Actor.Attributes[mngr.labels.backupName])
	}

	return nil
}

func (mngr *ContainerManager) syncBackupers(ctx context.Context) error {
	var opts events.ListOptions
	opts.Filters = filters.NewArgs()
	opts.Filters.Add("label", mngr.labels.backupName)

	for {
		eventChan, errChan := mngr.docker.Events(ctx, opts)

		err := mngr.initBackupers(ctx)
		if err != nil {
			return err
		}

	eventLoop:
		for {
			select {
			case event := <-eventChan:
				err := mngr.handleDockerEvent(ctx, event)
				if err != nil {
					return err
				}

			case err := <-errChan:
				if ctx.Err() != nil {
					return nil
				}

				if err == io.EOF {
					break eventLoop
				}

				return fmt.Errorf("error during listen for docker events: %w", err)

			case <-ctx.Done():
				return nil
			}

		}

	}
}

func (mngr *ContainerManager) getContainerByLabelValue(ctx context.Context, label, value string, searchAll bool) (*types.Container, error) {
	var listOpts container.ListOptions

	listOpts.Filters = filters.NewArgs()
	listOpts.Filters.Add("label", fmt.Sprintf("%s=%s", label, value))

	listOpts.All = searchAll

	cntrs, err := mngr.docker.ContainerList(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	if len(cntrs) > 1 {
		return nil, fmt.Errorf("containers with label %s=%s more than 1: %d", label, value, len(cntrs))
	}

	if len(cntrs) == 1 {
		return &cntrs[0], nil
	}

	return nil, nil
}

func (mngr *ContainerManager) createContainer(ctx context.Context, cfg *Template, tag string, cntrName string) (string, error) {
	buildInfo, cntrCfg, hstCfg, netCfg, err := cfg.CreateConfig(tag)
	if err != nil {
		return "", err
	}

	if buildInfo != nil {
		err = mngr.buildImage(ctx, buildInfo, cntrCfg.Image, false)
		if err != nil {
			return "", err
		}
	} else {
		err := mngr.pullImage(ctx, cntrCfg.Image, false)
		if err != nil {
			return "", err
		}
	}

	resp, err := mngr.docker.ContainerCreate(ctx, cntrCfg, hstCfg, netCfg, nil, cntrName)
	if err != nil {
		return "", err
	}

	cntrId := resp.ID

	for _, warn := range resp.Warnings {
		log.Println("WARN:", warn)
	}

	return cntrId, nil
}

func (mngr *ContainerManager) pullImage(ctx context.Context, tag string, force bool) error {
	needPull := true

	if !strings.Contains(tag, ":") {
		tag = tag + ":latest"
	}

	if !force {

		localImages, err := mngr.docker.ImageList(ctx, image.ListOptions{})
		if err != nil {
			return fmt.Errorf("image list failed: %w", err)
		}

	imgLoop:
		for _, localImg := range localImages {
			for _, localTag := range localImg.RepoTags {
				if localTag == tag {
					needPull = false
					break imgLoop
				}
			}
		}

		if !needPull {
			return nil
		}
	}

	log.Printf("pulling %s\n", tag)

	resp, err := mngr.docker.ImagePull(ctx, tag, image.PullOptions{})
	if resp != nil {
		defer resp.Close()
	}

	if err != nil || resp == nil {
		return err
	}

	dec := json.NewDecoder(resp)

	for {
		var line pullRespLine
		if err := dec.Decode(&line); err == io.EOF {
			break
		} else if err != nil {
			return fmt.Errorf("cant decode pull output as json - %w", err)
		}

		if len(line.Error) > 0 {
			fmt.Printf("build error: %s\n", line.Error)
			return errors.New(line.Error)
		}

		if len(line.Message) > 0 {
			fmt.Printf("pull: %s\n", line.Message)
		} else {
			fmt.Printf("pull: %s: %s %s", line.Id, line.Status, line.Progress)
		}

	}

	log.Println("successfully pulled", tag)

	return nil
}

func (mngr *ContainerManager) buildImage(ctx context.Context, buildInfo *BuildInfo, tag string, force bool) error {
	needBuild := true

	if !strings.Contains(tag, ":") {
		tag = tag + ":latest"
	}

	localImages, err := mngr.docker.ImageList(ctx, image.ListOptions{})
	if err != nil {
		return fmt.Errorf("image list failed: %w", err)
	}

imgLoop:
	for _, localImg := range localImages {
		for _, localTag := range localImg.RepoTags {
			if localTag == tag {
				needBuild = false
				break imgLoop
			}
		}
	}

	if !needBuild && !force {
		return nil
	}

	for _, dependencyImage := range buildInfo.DependentBuilds {
		depBuildInfo := &BuildInfo{
			Context:    dependencyImage.Context,
			Dockerfile: dependencyImage.Dockerfile,
			Args:       dependencyImage.Args,
		}

		err := mngr.buildImage(ctx, depBuildInfo, dependencyImage.Tag, force)
		if err != nil {
			return fmt.Errorf("dependency (%s) build failed: %w", dependencyImage.Tag, err)
		}
	}

	log.Println("start building", tag)

	opts := types.ImageBuildOptions{
		Version: types.BuilderBuildKit,
	}

	if len(buildInfo.Args) > 0 {
		buildArgsPtr := make(map[string]*string)

		for k, v := range buildInfo.Args {
			v := v
			buildArgsPtr[k] = &v
		}

		opts.BuildArgs = buildArgsPtr
	}

	if mngr.conf.BuilderV1 {
		opts.Version = types.BuilderV1
	}

	if len(buildInfo.Dockerfile) > 0 {
		opts.Dockerfile = buildInfo.Dockerfile
	}

	opts.Tags = []string{tag}

	buildCtx := "."

	if len(buildInfo.Context) > 0 {
		buildCtx = buildInfo.Context
	}

	var archive bytes.Buffer

	err = tarGz(buildCtx, &archive)
	if err != nil {
		return fmt.Errorf("build error: %w", err)
	}

	resp, err := mngr.docker.ImageBuild(ctx, &archive, opts)
	if resp.Body != nil {
		defer resp.Body.Close()
	}

	if err != nil || resp.Body == nil {
		return err
	}

	dec := json.NewDecoder(resp.Body)

	for {
		var line buildRespLine
		if err := dec.Decode(&line); err == io.EOF {
			break
		} else if err != nil {
			return fmt.Errorf("cant decode build output as json - %w", err)
		}

		if len(line.Error) > 0 {
			fmt.Printf("build error: %s\n", line.Error)

			return errors.New(line.Error)

		}

		if line.Aux != nil {
			if s, ok := line.Aux.(string); ok {
				msgData, err := base64.StdEncoding.DecodeString(s)
				if err != nil {
					return fmt.Errorf("failed to decode base64 aux (%v): %w", line, err)
				}

				var msg controlapi.StatusResponse
				err = proto.Unmarshal(msgData, &msg)
				if err != nil {
					return fmt.Errorf("failed to decode protobuf aux  (%v): %w", line, err)
				}

				for _, v := range msg.Vertexes {
					fmt.Printf("buildkit: %v\n", v.Name)
				}
				for _, v := range msg.Logs {
					fmt.Printf("buildkit: %v", string(v.Msg))
				}
				for _, v := range msg.Statuses {
					fmt.Printf("buildkit: %v\n", v.ID)
				}
				for _, v := range msg.Warnings {
					fmt.Printf("buildkit warn: %v\n", string(v.Short))
				}
			}
		}

		if len(line.Message) > 0 {
			fmt.Printf("build msg: %s\n", line.Message)
		}

		if len(line.Stream) > 0 {
			fmt.Printf("build: %s\n", line.Stream)
		}
	}

	log.Println("successfully built", tag)

	return nil
}

func (mngr *ContainerManager) startBackuper(ctx context.Context, cfg *Template, cntrName string) error {
	cntrId, err := mngr.createContainer(ctx, cfg, mngr.conf.BackupTag, cntrName)
	if err != nil {
		return err
	}

	err = mngr.docker.ContainerStart(ctx, cntrId, container.StartOptions{})
	if err != nil {
		return err
	}

	return nil
}

func (mngr *ContainerManager) waitForStop(ctx context.Context, cntrId string) error {
	var opts events.ListOptions
	opts.Filters = filters.NewArgs()
	opts.Filters.Add("id", cntrId)
	opts.Filters.Add("type", "container")
	opts.Filters.Add("event", "die")

	eventChan, errChan := mngr.docker.Events(ctx, opts)

	select {
	case _, ok := <-eventChan:
		if ok {
			return nil
		} else {
			return fmt.Errorf("error during listen for docker events: %w", ctx.Err())
		}

	case err := <-errChan:
		return fmt.Errorf("error during listen for docker events: %w", err)
	}
}

func getContainerLabel(cntr *types.Container, label string) string {
	if val, ok := cntr.Labels[label]; ok {
		return val
	}

	return ""
}

func containerIsAlive(cntr *types.Container) bool {
	return cntr != nil && (cntr.State == ContainerStatusRunning || cntr.State == ContainerStatusRestarting)
}

func tarGz(src string, writer io.Writer) error {

	// ensure the src actually exists before trying to tar it
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("unable to tar file %s - %v", src, err.Error())
	}

	gzw := gzip.NewWriter(writer)
	defer gzw.Close()

	tw := tar.NewWriter(gzw)
	defer tw.Close()

	// walk path
	return filepath.Walk(src, func(file string, fi os.FileInfo, err error) error {

		// return on any error
		if err != nil {
			return fmt.Errorf("file %s error: %w", file, err)
		}

		// return on non-regular files (thanks to [kumo](https://medium.com/@komuw/just-like-you-did-fbdd7df829d3) for this suggested update)
		if !fi.Mode().IsRegular() {
			return nil
		}

		// create a new dir/file header
		header, err := tar.FileInfoHeader(fi, fi.Name())
		if err != nil {
			return fmt.Errorf("file %s error: %w", file, err)
		}

		// update the name to correctly reflect the desired destination when untaring
		header.Name = strings.TrimPrefix(strings.Replace(file, src, "", -1), string(filepath.Separator))

		// write the header
		if err := tw.WriteHeader(header); err != nil {
			return fmt.Errorf("file %s error: %w", file, err)
		}

		// open files for taring
		f, err := os.Open(file)
		if err != nil {
			return fmt.Errorf("file %s error: %w", file, err)
		}

		// copy file data into tar writer
		if _, err := io.Copy(tw, f); err != nil {
			f.Close()
			return fmt.Errorf("file %s error: %w", file, err)
		}

		// manually close here after each file operation; defering would cause each file close
		// to wait until all operations have completed.
		f.Close()

		return nil
	})
}
