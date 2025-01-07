package internal

import (
	"context"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	// "github.com/anpavlov/docker-backup-mastro.git/internal"
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

func genBackuper(mngr *ContainerManager, name string) types.Container {
	tmpl := mngr.tmpls.Backuper.Overlay(&Template{
		Labels:  map[string]string{mngr.labels.backuperName: name},
		Volumes: []string{"/data:/data:ro"},
	})
	hash := tmpl.Hash()

	return types.Container{
		ID: "backuperid" + name,
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
		mngr:            mngr,
		docker:          docker,
		liveBackupers:   make(map[string]types.Container),
		liveBackupCntrs: make(map[string]types.Container),
	}

	for _, name := range backupCntrs {
		tst.liveBackupCntrs[name] = genBackupCntr(mngr, name)
	}

	for _, name := range backupers {
		tst.liveBackupers[name] = genBackuper(mngr, name)
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
			_, ok := cntr.Labels[label+"="+name]
			return ok
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

func (tm *testMngr) resetExpectCntrList() {
	for _, call := range tm.listCalls {
		call.Unset()
	}
}

func (tm *testMngr) expectListenEvents() {
	tm.eventsChan = make(chan events.Message)
	tm.errChan = make(chan error)

	tm.docker.EXPECT().Events(mock.Anything, mock.Anything).Return(tm.eventsChan, tm.errChan)
}

func (tm *testMngr) startBackupCntr(name string) {
	tm.liveBackupCntrs[name] = genBackupCntr(tm.mngr, name)

	tm.resetExpectCntrList()
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

	tm.resetExpectCntrList()
	tm.expectCntrList()

	tm.eventsChan <- events.Message{
		Action: events.ActionDie,
		Actor: events.Actor{
			Attributes: map[string]string{tm.mngr.labels.backupName: name},
		},
	}
}

func (tm *testMngr) expectBackuperCreateAndStart(t *testing.T, tmpl *Template, tag string, name string) {
	_, cntrCfg, hstCfg, netCfg, err := tmpl.CreateConfig(tag)
	require.NoError(t, err)

	tmpl = tmpl.Overlay(&Template{
		Labels:  map[string]string{tm.mngr.labels.backuperName: name},
		Volumes: []string{"/data:/data:ro"},
	})
	hash := tmpl.Hash()

	if cntrCfg.Labels == nil {
		cntrCfg.Labels = make(map[string]string)
	}
	cntrCfg.Labels[tm.mngr.labels.backuperName] = name
	cntrCfg.Labels[tm.mngr.labels.backuperConsistencyHash] = hash

	hstCfg.Binds = append(hstCfg.Binds, "/data:/data:ro")

	tm.docker.EXPECT().ContainerCreate(mock.Anything, cntrCfg, hstCfg, netCfg, mock.Anything, mock.Anything).Return(container.CreateResponse{ID: "hello"}, nil)
	tm.docker.EXPECT().ContainerStart(mock.Anything, "hello", mock.Anything).Return(nil)

	// tm.liveBackupers[name] = genBackuper(tm.mngr, name)
	// tm.resetExpectCntrList()
	// tm.expectCntrList()
}

func (tm *testMngr) expectImageList(tags []string) {
	imgs := []image.Summary{}

	for _, tag := range tags {
		imgs = append(imgs, image.Summary{RepoTags: []string{tag}})
	}

	tm.docker.EXPECT().ImageList(mock.Anything, mock.Anything).Return(imgs, nil)
}

func (tm *testMngr) expectBackuperRemove(t *testing.T, name string) {
	tm.docker.EXPECT().ContainerStop(mock.Anything, "backuperid"+name, mock.Anything).Return(nil)
	tm.docker.EXPECT().ContainerRemove(mock.Anything, "backuperid"+name, mock.Anything).Return(nil)
}

func (tm *testMngr) expectBuild(tag string) {
	resp := strings.NewReader("")
	tm.docker.EXPECT().ImageBuild(mock.Anything, mock.Anything, types.ImageBuildOptions{Tags: []string{tag}}).Return(types.ImageBuildResponse{Body: io.NopCloser(resp)}, nil)
}

func (tm *testMngr) expectPull(tag string) {
	resp := strings.NewReader("")
	tm.docker.EXPECT().ImagePull(mock.Anything, tag, mock.Anything).Return(io.NopCloser(resp), nil)
}

func TestNewBackuperOnStart(t *testing.T) {
	tm := newTestMngr(t, []string{"example"}, nil, UserTemplates{Backuper: &Template{Image: "alpine"}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tm.expectListenEvents()
	tm.expectImageList([]string{"alpine:latest"})

	tm.expectBackuperCreateAndStart(t, tm.mngr.tmpls.Backuper, tm.mngr.labels.backuperTag, "example")

	go func() {
		require.NoError(t, tm.mngr.Run(ctx))
	}()

	<-time.After(time.Second)
}

// TODO test restore when taget backup is created but not run

func TestNewBackupOnline(t *testing.T) {
	tm := newTestMngr(t, nil, nil, UserTemplates{Backuper: &Template{Image: "alpine"}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tm.expectListenEvents()
	tm.expectImageList([]string{"alpine:latest"})

	go func() {
		require.NoError(t, tm.mngr.Run(ctx))
	}()

	<-time.After(time.Second)

	tm.expectBackuperCreateAndStart(t, tm.mngr.tmpls.Backuper, tm.mngr.labels.backuperTag, "example")
	tm.startBackupCntr("example")

	<-time.After(time.Second)
}

func TestSyncBackuperNoop(t *testing.T) {
	tm := newTestMngr(t, []string{"example"}, []string{"example"}, UserTemplates{Backuper: &Template{Image: "alpine"}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tm.expectListenEvents()

	go func() {
		require.NoError(t, tm.mngr.Run(ctx))
	}()

	<-time.After(time.Second)
}

func TestDropDanglingBackuper(t *testing.T) {
	tm := newTestMngr(t, nil, []string{"example"}, UserTemplates{Backuper: &Template{Image: "alpine"}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tm.expectListenEvents()

	tm.expectBackuperRemove(t, "example")

	go func() {
		require.NoError(t, tm.mngr.Run(ctx))
	}()

	<-time.After(time.Second)
}

func TestBuildBackuper(t *testing.T) {
	tm := newTestMngr(t, []string{"example"}, nil, UserTemplates{Backuper: &Template{Build: BuildInfo{Data: struct {
		Context    string
		Dockerfile string
	}{Context: "."}}}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tm.expectListenEvents()
	tm.expectImageList(nil)

	tm.expectBuild(tm.mngr.labels.backuperTag + ":latest")

	tm.expectBackuperCreateAndStart(t, tm.mngr.tmpls.Backuper, tm.mngr.labels.backuperTag, "example")

	go func() {
		require.NoError(t, tm.mngr.Run(ctx))
	}()

	<-time.After(time.Second)
}

func TestNoRebuildBackuper(t *testing.T) {
	tm := newTestMngr(t, []string{"example"}, nil, UserTemplates{Backuper: &Template{Build: BuildInfo{Data: struct {
		Context    string
		Dockerfile string
	}{Context: "."}}}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tm.expectListenEvents()
	tm.expectImageList([]string{tm.mngr.labels.backuperTag + ":latest"})

	tm.expectBackuperCreateAndStart(t, tm.mngr.tmpls.Backuper, tm.mngr.labels.backuperTag, "example")

	go func() {
		require.NoError(t, tm.mngr.Run(ctx))
	}()

	<-time.After(time.Second)
}
