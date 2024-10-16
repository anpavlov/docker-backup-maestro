package main

import (
	"context"
	"fmt"
	"io"
	"log"

	"github.com/anpavlov/docker-backup-mastro.git/backuper"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
)

const (
	ContainerStatusRunning    = "running"
	ContainerStatusRestarting = "restarting"
)

func (mngr *ContainerManager) listContainersWithLabel(ctx context.Context, label string) ([]types.Container, error) {
	var opts container.ListOptions

	opts.Filters = filters.NewArgs()
	opts.Filters.Add("label", label)

	return mngr.docker.ContainerList(ctx, opts)
}

func (mngr *ContainerManager) handleDockerEvent(ctx context.Context, event events.Message) error {
	if event.Action == events.ActionStart {
		return mngr.createBackuper(ctx, event.Actor.Attributes[labelBackupName])
	} else if event.Action == events.ActionDie {
		return mngr.dropBackuper(ctx, event.Actor.Attributes[labelBackupName])
	}

	return nil
}

func (mngr *ContainerManager) listenEvents(ctx context.Context) error {
	var opts events.ListOptions
	opts.Filters = filters.NewArgs()
	opts.Filters.Add("label", labelBackup)

	for {
		eventChan, errChan := mngr.docker.Events(ctx, opts)

		select {
		case <-ctx.Done():
			return nil
		case <-mngr.initDone:
		}

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
					break
				}

				return fmt.Errorf("error during listen for docker events: %w", err)

			case <-ctx.Done():
				return nil
			}

		}
	}
}

// func (mngr *ContainerManager) getContainerByID(ctx context.Context, id string) (res types.Container, err error) {
// 	cntrs, err := mngr.docker.ContainerList(ctx, container.ListOptions{Filters: filters.NewArgs(filters.KeyValuePair{Key: "id", Value: id})})
// 	if err != nil {
// 		return
// 	}

// 	if len(cntrs) != 1 {
// 		err = fmt.Errorf("container by id returned not 1 containger: %d", len(cntrs))
// 		return
// 	}

// 	res = cntrs[0]
// 	return
// }

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

func (mngr *ContainerManager) startContainer(ctx context.Context, cfg *backuper.Template) error {
	cntrCfg, hstCfg, netCfg, err := cfg.CreateConfig()
	if err != nil {
		return err
	}

	resp, err := mngr.docker.ContainerCreate(ctx, cntrCfg, hstCfg, netCfg, nil, "")
	if err != nil {
		return err
	}

	cntrId := resp.ID

	for _, warn := range resp.Warnings {
		log.Println("WARN:", warn)
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
	return cntr.Status == ContainerStatusRunning || cntr.Status == ContainerStatusRestarting
}
