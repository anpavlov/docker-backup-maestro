package internal

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
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
)

const (
	ContainerStatusRunning    = "running"
	ContainerStatusRestarting = "restarting"
)

type buildRespLine struct {
	Message     string
	Error       string
	ErrorDetail string
	Stream      string
}

type pullRespLine struct {
	Message  string
	Status   string
	Id       string
	Progress string
}

func (mngr *ContainerManager) listContainersWithLabel(ctx context.Context, label string, searchAll bool) ([]types.Container, error) {
	var opts container.ListOptions

	opts.Filters = filters.NewArgs()
	opts.Filters.Add("label", label)

	opts.All = searchAll

	return mngr.docker.ContainerList(ctx, opts)
}

func (mngr *ContainerManager) handleDockerEvent(ctx context.Context, event events.Message) error {
	if event.Action == events.ActionStart {
		return mngr.createBackuper(ctx, event.Actor.Attributes[mngr.labels.backupName])
	} else if event.Action == events.ActionDie {
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

func (mngr *ContainerManager) createContainer(ctx context.Context, cfg *Template, tag string) (string, error) {
	buildInfo, cntrCfg, hstCfg, netCfg, err := cfg.CreateConfig(tag)
	if err != nil {
		return "", err
	}

	if buildInfo != nil {
		err = mngr.buildImage(ctx, buildInfo, cntrCfg.Image)
		if err != nil {
			return "", err
		}
	} else {
		err := mngr.pullImage(ctx, cntrCfg.Image)
		if err != nil {
			return "", err
		}
	}

	resp, err := mngr.docker.ContainerCreate(ctx, cntrCfg, hstCfg, netCfg, nil, "")
	if err != nil {
		return "", err
	}

	cntrId := resp.ID

	for _, warn := range resp.Warnings {
		log.Println("WARN:", warn)
	}

	return cntrId, nil
}

func (mngr *ContainerManager) pullImage(ctx context.Context, tag string) error {
	needPull := true

	if strings.Index(tag, ":") == -1 {
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
				needPull = false
				break imgLoop
			}
		}
	}

	if !needPull {
		return nil
	}

	resp, err := mngr.docker.ImagePull(ctx, tag, image.PullOptions{})
	if resp != nil {
		defer resp.Close()
	}

	dec := json.NewDecoder(resp)

	for {
		var line pullRespLine
		if err := dec.Decode(&line); err == io.EOF {
			break
		} else if err != nil {
			return fmt.Errorf("cant decode pull output as json - %w", err)
		}

		if len(line.Message) > 0 {
			fmt.Printf("pull: %s\n", line.Message)
		} else {
			fmt.Printf("pull: %s: %s %s", line.Id, line.Status, line.Progress)
		}

	}

	return err
}

func (mngr *ContainerManager) buildImage(ctx context.Context, buildInfo *BuildInfo, tag string) error {
	needBuild := true

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

	if !needBuild {
		return nil
	}

	opts := types.ImageBuildOptions{}

	if len(buildInfo.Data.Dockerfile) > 0 {
		opts.Dockerfile = buildInfo.Data.Dockerfile
	}

	opts.Tags = []string{tag}

	buildCtx := "."

	if len(buildInfo.Data.Context) > 0 {
		buildCtx = buildInfo.Data.Context
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

	dec := json.NewDecoder(resp.Body)

	for {
		var line buildRespLine
		if err := dec.Decode(&line); err == io.EOF {
			break
		} else if err != nil {
			return fmt.Errorf("cant decode build output as json - %w", err)
		}

		if len(line.Error) > 0 {
			fmt.Printf("build error: %s %s\n", line.Error, line.ErrorDetail)
		}

		if len(line.Message) > 0 {
			fmt.Printf("build msg: %s\n", line.Message)
		}

		if len(line.Stream) > 0 {
			fmt.Printf("build: %s", line.Stream)
		}
	}

	return err
}

func (mngr *ContainerManager) startBackuper(ctx context.Context, cfg *Template) error {
	cntrId, err := mngr.createContainer(ctx, cfg, mngr.labels.backuperTag)
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
	return cntr != nil && (cntr.Status == ContainerStatusRunning || cntr.Status == ContainerStatusRestarting)
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
