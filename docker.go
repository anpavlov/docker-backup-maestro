package main

import (
	"context"
	"fmt"
	"io"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
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

func (mngr *ContainerManager) getBackupContainerByName(ctx context.Context, name string) (res types.Container, err error) {
	var listOpts container.ListOptions

	listOpts.Filters = filters.NewArgs()
	listOpts.Filters.Add("label", fmt.Sprintf("%s=%s", labelBackupName, name))

	cntrs, err := mngr.docker.ContainerList(ctx, listOpts)
	if err != nil {
		return
	}

	if len(cntrs) != 1 {
		err = fmt.Errorf("backup container by name returned not 1 containger: %d", len(cntrs))
		return
	}

	res = cntrs[0]
	return
}

func getContainerLabel(cntr types.Container, label string) string {
	if val, ok := cntr.Labels[label]; ok {
		return val
	}

	return ""
}
