package internal

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/anpavlov/docker-backup-mastro.git/mocks"
	"github.com/caarlos0/env/v11"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tiendc/go-deepcopy"
)

const testDataPath = "/data"

type CallUnsetter interface {
	Unset() *mock.Call
}

type testMngr struct {
	mngr               *ContainerManager
	docker             *mocks.DockerApi
	liveBackupers      map[string]types.Container
	liveBackupCntrs    map[string]types.Container
	stoppedBackupers   map[string]types.Container
	stoppedBackupCntrs map[string]types.Container

	listCalls []CallUnsetter

	eventsChan chan events.Message
	errChan    chan error
}

func genBackupCntr(mngr *ContainerManager, name string) types.Container {
	return types.Container{
		ID: "backupid" + name,
		Labels: map[string]string{
			mngr.labels.backupName: name,
			mngr.labels.backupPath: testDataPath,
		},
	}
}

func genOnlineBackuper(mngr *ContainerManager, name string) types.Container {
	tmpl := mngr.tmpls.Backuper.Overlay(&Template{
		Labels:  map[string]string{mngr.labels.backuperName: name},
		Volumes: []string{"/data:/data:ro"},
	})
	hash := tmpl.Hash()

	return types.Container{
		ID:     "backuperid" + name,
		Status: ContainerStatusRunning,
		Labels: map[string]string{
			mngr.labels.backuperName:            name,
			mngr.labels.backuperConsistencyHash: hash,
		},
	}

}

func newTestMngr(t *testing.T, backupCntrs []string, backupers []string, tmpls UserTemplates) testMngr {
	var cfg Config
	err := env.ParseWithOptions(&cfg, env.Options{Environment: map[string]string{}})
	require.NoError(t, err)

	docker := mocks.NewDockerApi(t)

	if tmpls.Backuper == nil {
		t.Fatal("Backuper template is empty")
	}

	if tmpls.ForceBackup == nil {
		tmpls.ForceBackup = &Template{}
		require.NoError(t, deepcopy.Copy(tmpls.ForceBackup, tmpls.Backuper))
	}

	if tmpls.Restore == nil {
		tmpls.Restore = &Template{}
		require.NoError(t, deepcopy.Copy(tmpls.Restore, tmpls.Backuper))
	}

	mngr := NewContainerManager(docker, tmpls, cfg)

	tst := testMngr{
		mngr:               mngr,
		docker:             docker,
		liveBackupers:      make(map[string]types.Container),
		liveBackupCntrs:    make(map[string]types.Container),
		stoppedBackupers:   make(map[string]types.Container),
		stoppedBackupCntrs: make(map[string]types.Container),
	}

	for _, name := range backupCntrs {
		tst.liveBackupCntrs[name] = genBackupCntr(mngr, name)
	}

	for _, name := range backupers {
		tst.liveBackupers[name] = genOnlineBackuper(mngr, name)
	}

	tst.expectCntrList()

	return tst
}

func (tm *testMngr) expectCntrList() {
	liveBackupCntrs := slices.Collect(maps.Values(tm.liveBackupCntrs))
	liveBackupers := slices.Collect(maps.Values(tm.liveBackupers))
	stoppedBackupCntrs := slices.Collect(maps.Values(tm.stoppedBackupCntrs))
	stoppedBackupers := slices.Collect(maps.Values(tm.stoppedBackupers))

	tm.listCalls = append(tm.listCalls, tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
		Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backuperName}),
	}).Return(liveBackupers, nil).Maybe())

	tm.listCalls = append(tm.listCalls, tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backuperName}),
	}).Return(append(liveBackupers, stoppedBackupers...), nil).Maybe())

	tm.listCalls = append(tm.listCalls, tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
		Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backupName}),
	}).Return(liveBackupCntrs, nil).Maybe())

	tm.listCalls = append(tm.listCalls, tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backupName}),
	}).Return(append(liveBackupCntrs, stoppedBackupCntrs...), nil).Maybe())

	filterLabelVal := func(label string, name string) func(cntr types.Container) bool {
		return func(cntr types.Container) bool {
			val, ok := cntr.Labels[label]
			return ok && val == name
		}
	}

	for name, cntr := range tm.liveBackupCntrs {
		tm.listCalls = append(tm.listCalls, tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
			Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backupName + "=" + name}),
		}).Return([]types.Container{cntr}, nil).Maybe())

		tm.listCalls = append(tm.listCalls, tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
			All:     true,
			Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backupName + "=" + name}),
		}).Return([]types.Container{cntr}, nil).Maybe())

		// if no backuper (live or stopped) configured with same name, expect backuper list with this name to empty list
		if slices.IndexFunc(liveBackupers, filterLabelVal(tm.mngr.labels.backuperName, name)) == -1 &&
			slices.IndexFunc(stoppedBackupers, filterLabelVal(tm.mngr.labels.backuperName, name)) == -1 {
			tm.listCalls = append(tm.listCalls, tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
				Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backuperName + "=" + name}),
			}).Return([]types.Container{}, nil).Maybe())

			tm.listCalls = append(tm.listCalls, tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
				All:     true,
				Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backuperName + "=" + name}),
			}).Return([]types.Container{}, nil).Maybe())
		}
	}

	for name, cntr := range tm.stoppedBackupCntrs {
		tm.listCalls = append(tm.listCalls, tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
			Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backupName + "=" + name}),
		}).Return([]types.Container{}, nil).Maybe())

		tm.listCalls = append(tm.listCalls, tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
			All:     true,
			Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backupName + "=" + name}),
		}).Return([]types.Container{cntr}, nil).Maybe())

		if slices.IndexFunc(liveBackupers, filterLabelVal(tm.mngr.labels.backuperName, name)) == -1 &&
			slices.IndexFunc(stoppedBackupers, filterLabelVal(tm.mngr.labels.backuperName, name)) == -1 {
			tm.listCalls = append(tm.listCalls, tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
				Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backuperName + "=" + name}),
			}).Return([]types.Container{}, nil).Maybe())

			tm.listCalls = append(tm.listCalls, tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
				All:     true,
				Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backuperName + "=" + name}),
			}).Return([]types.Container{}, nil).Maybe())
		}
	}

	for name, cntr := range tm.liveBackupers {
		tm.listCalls = append(tm.listCalls, tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
			Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backuperName + "=" + name}),
		}).Return([]types.Container{cntr}, nil).Maybe())

		tm.listCalls = append(tm.listCalls, tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
			All:     true,
			Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backuperName + "=" + name}),
		}).Return([]types.Container{cntr}, nil).Maybe())

		if slices.IndexFunc(liveBackupCntrs, filterLabelVal(tm.mngr.labels.backupName, name)) == -1 &&
			slices.IndexFunc(stoppedBackupCntrs, filterLabelVal(tm.mngr.labels.backupName, name)) == -1 {
			tm.listCalls = append(tm.listCalls, tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
				Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backupName + "=" + name}),
			}).Return([]types.Container{}, nil).Maybe())

			tm.listCalls = append(tm.listCalls, tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
				All:     true,
				Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backupName + "=" + name}),
			}).Return([]types.Container{}, nil).Maybe())
		}
	}

	for name, cntr := range tm.stoppedBackupers {
		tm.listCalls = append(tm.listCalls, tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
			Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backuperName + "=" + name}),
		}).Return([]types.Container{}, nil).Maybe())

		tm.listCalls = append(tm.listCalls, tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
			All:     true,
			Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backuperName + "=" + name}),
		}).Return([]types.Container{cntr}, nil).Maybe())

		if slices.IndexFunc(liveBackupCntrs, filterLabelVal(tm.mngr.labels.backupName, name)) == -1 &&
			slices.IndexFunc(stoppedBackupCntrs, filterLabelVal(tm.mngr.labels.backupName, name)) == -1 {
			tm.listCalls = append(tm.listCalls, tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
				Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backupName + "=" + name}),
			}).Return([]types.Container{}, nil).Maybe())

			tm.listCalls = append(tm.listCalls, tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
				All:     true,
				Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backupName + "=" + name}),
			}).Return([]types.Container{}, nil).Maybe())
		}
	}
}

func (tm *testMngr) resetExpectCallList() {
	for _, call := range tm.listCalls {
		call.Unset()
	}

	tm.listCalls = nil
}

func (tm *testMngr) expectListenEvents() {
	tm.eventsChan = make(chan events.Message)
	tm.errChan = make(chan error)

	tm.docker.EXPECT().Events(mock.Anything, mock.Anything).Return(tm.eventsChan, tm.errChan).Once()
}

func (tm *testMngr) startBackupCntr(name string) {
	tm.liveBackupCntrs[name] = genBackupCntr(tm.mngr, name)

	tm.resetExpectCallList()
	tm.expectCntrList()

	tm.eventsChan <- events.Message{
		Action: events.ActionStart,
		Actor: events.Actor{
			Attributes: map[string]string{tm.mngr.labels.backupName: name},
		},
	}
}

func (tm *testMngr) removeBackupCntr(name string) {
	delete(tm.liveBackupCntrs, name)

	tm.resetExpectCallList()
	tm.expectCntrList()

	tm.eventsChan <- events.Message{
		Action: events.ActionDie,
		Actor: events.Actor{
			Attributes: map[string]string{tm.mngr.labels.backupName: name},
		},
	}
}

func (tm *testMngr) expectBackuperCreateAndStart(t *testing.T, name string, labels map[string]string, overlay *Template) {
	if labels != nil {
		cntr := tm.liveBackupCntrs["example"]
		cntr.Labels = labels
		tm.liveBackupCntrs["example"] = cntr

		tm.resetExpectCallList()
		tm.expectCntrList()
	}

	tmpl := tm.mngr.tmpls.Backuper
	if overlay != nil {
		tmpl = tmpl.Overlay(overlay)
	} else {
		tmpl = tmpl.Overlay(&Template{
			Labels:  map[string]string{tm.mngr.labels.backuperName: name},
			Volumes: []string{"/data:/data:ro"},
		})
	}
	hash := tmpl.Hash()

	_, cntrCfg, hstCfg, netCfg, err := tmpl.CreateConfig(tm.mngr.conf.BackupTag)
	require.NoError(t, err)

	if cntrCfg.Labels == nil {
		cntrCfg.Labels = make(map[string]string)
	}

	cntrCfg.Labels[tm.mngr.labels.backuperName] = name
	cntrCfg.Labels[tm.mngr.labels.backuperConsistencyHash] = hash

	tm.docker.EXPECT().ContainerCreate(mock.Anything, cntrCfg, hstCfg, netCfg, mock.Anything, fmt.Sprintf("docker-backup-maestro.backup_%s", name)).Return(container.CreateResponse{ID: "hello"}, nil).Once()
	tm.docker.EXPECT().ContainerStart(mock.Anything, "hello", mock.Anything).Return(nil).Once()
}

func (tm *testMngr) expectImageList(tags []string) {
	imgs := []image.Summary{}

	for _, tag := range tags {
		imgs = append(imgs, image.Summary{RepoTags: []string{tag}})
	}

	tm.docker.EXPECT().ImageList(mock.Anything, mock.Anything).Return(imgs, nil)
}

func (tm *testMngr) expectBackuperRemove(name string) {
	tm.docker.EXPECT().ContainerStop(mock.Anything, "backuperid"+name, mock.Anything).Return(nil).Once()
	tm.docker.EXPECT().ContainerRemove(mock.Anything, "backuperid"+name, mock.Anything).Run(func(_ context.Context, _ string, _ container.RemoveOptions) {
		delete(tm.liveBackupers, name)

		tm.resetExpectCallList()
		tm.expectCntrList()
	}).Return(nil).Once()
}

func (tm *testMngr) expectBuild(tag string) {
	resp := strings.NewReader("")
	tm.docker.EXPECT().ImageBuild(mock.Anything, mock.Anything, types.ImageBuildOptions{Tags: []string{tag}}).Return(types.ImageBuildResponse{Body: io.NopCloser(resp)}, nil).Once()
}

func (tm *testMngr) expectPull(tag string) {
	resp := strings.NewReader("")
	tm.docker.EXPECT().ImagePull(mock.Anything, tag, mock.Anything).Return(io.NopCloser(resp), nil).Once()
}

func (tm *testMngr) expectBackuperStop(name string) {
	tm.docker.EXPECT().ContainerStop(mock.Anything, "backuperid"+name, mock.Anything).Run(func(ctx context.Context, containerID string, options container.StopOptions) {
		tm.stoppedBackupers[name] = tm.liveBackupers[name]
		delete(tm.liveBackupers, name)

		tm.resetExpectCallList()
		tm.expectCntrList()
	}).Return(nil).Once()
}

func (tm *testMngr) expectBackuperStart(name string) {
	tm.docker.EXPECT().ContainerStart(mock.Anything, "backuperid"+name, mock.Anything).Run(func(ctx context.Context, containerID string, options container.StartOptions) {
		tm.liveBackupers[name] = tm.stoppedBackupers[name]
		delete(tm.stoppedBackupers, name)

		tm.resetExpectCallList()
		tm.expectCntrList()
	}).Return(nil).Once()
}

func (tm *testMngr) expectRestoreCreateAndStart(t *testing.T, name string) {
	tmpl := tm.mngr.tmpls.Restore
	_, cntrCfg, hstCfg, netCfg, err := tmpl.CreateConfig(tm.mngr.conf.RestoreTag)
	require.NoError(t, err)

	if cntrCfg.Labels == nil {
		cntrCfg.Labels = make(map[string]string)
	}
	cntrCfg.Labels[tm.mngr.labels.restore] = name
	hstCfg.AutoRemove = true

	hstCfg.Binds = append(hstCfg.Binds, "/data:/data")

	tm.docker.EXPECT().ContainerCreate(mock.Anything, cntrCfg, hstCfg, netCfg, mock.Anything, fmt.Sprintf("docker-backup-maestro.restore_%s", name)).Return(container.CreateResponse{ID: "restoreid" + name}, nil).Once()
	tm.docker.EXPECT().ContainerStart(mock.Anything, "restoreid"+name, mock.Anything).Return(nil).Once()
}
