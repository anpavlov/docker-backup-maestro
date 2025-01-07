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

type testMngr struct {
	mngr               *ContainerManager
	docker             *mocks.DockerApi
	liveBackupers      map[string]types.Container
	liveBackupCntrs    map[string]types.Container
	stoppedBackupers   map[string]types.Container
	stoppedBackupCntrs map[string]types.Container

	eventsChan chan events.Message
	errChan    chan error
}

func genBackupCntr(mngr *ContainerManager, name string) types.Container {
	return types.Container{
		Labels: map[string]string{
			mngr.labels.backupName: name,
			mngr.labels.backupPath: testDataPath,
		},
	}
}

func genBackuper(mngr *ContainerManager, name string) types.Container {
	hash := mngr.tmpls.Backuper.Hash()

	return types.Container{
		Labels: map[string]string{
			mngr.labels.backuperName:            name,
			mngr.labels.backuperConsistencyHash: hash,
		},
	}

}

func newTestMngr(t *testing.T, backupCntrs []string, backupers []string, tmpls *UserTemplates) testMngr {
	var cfg Config
	err := env.ParseWithOptions(&cfg, env.Options{Environment: map[string]string{}})
	require.NoError(t, err)

	docker := mocks.NewDockerApi(t)

	defaultTmpl := Template{
		Image: "alpine",
	}
	if tmpls == nil {
		tmpls = &UserTemplates{
			Backuper:    &Template{},
			Restore:     &Template{},
			ForceBackup: &Template{},
		}

		require.NoError(t, deepcopy.Copy(tmpls.Backuper, defaultTmpl))
		require.NoError(t, deepcopy.Copy(tmpls.Restore, defaultTmpl))
		require.NoError(t, deepcopy.Copy(tmpls.ForceBackup, defaultTmpl))
	} else {
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
	}

	mngr := NewContainerManager(docker, *tmpls, cfg)

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

	return tst
}

func (tm *testMngr) expectCntrList() {
	liveBackupCntrs := slices.Collect(maps.Values(tm.liveBackupCntrs))
	liveBackupers := slices.Collect(maps.Values(tm.liveBackupers))
	stoppedBackupCntrs := slices.Collect(maps.Values(tm.stoppedBackupCntrs))
	stoppedBackupers := slices.Collect(maps.Values(tm.stoppedBackupers))

	tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
		Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backuperName}),
	}).Return(liveBackupers, nil).Maybe()

	tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backuperName}),
	}).Return(append(liveBackupers, stoppedBackupers...), nil).Maybe()

	tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
		Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backupName}),
	}).Return(liveBackupCntrs, nil).Maybe()

	tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backupName}),
	}).Return(append(liveBackupCntrs, stoppedBackupCntrs...), nil).Maybe()

	filterLabelVal := func(label string, name string) func(cntr types.Container) bool {
		return func(cntr types.Container) bool {
			_, ok := cntr.Labels[label+"="+name]
			return ok
		}
	}

	for name, cntr := range tm.liveBackupCntrs {
		tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
			Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backupName + "=" + name}),
		}).Return([]types.Container{cntr}, nil).Maybe()

		tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
			All:     true,
			Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backupName + "=" + name}),
		}).Return([]types.Container{cntr}, nil).Maybe()

		// if no backuper (live or stopped) configured with same name, expect backuper list with this name to empty list
		if slices.IndexFunc(liveBackupers, filterLabelVal(tm.mngr.labels.backuperName, name)) == -1 &&
			slices.IndexFunc(stoppedBackupers, filterLabelVal(tm.mngr.labels.backuperName, name)) == -1 {
			tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
				Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backuperName + "=" + name}),
			}).Return([]types.Container{}, nil).Maybe()

			tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
				All:     true,
				Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backuperName + "=" + name}),
			}).Return([]types.Container{}, nil).Maybe()
		}
	}

	for name, cntr := range tm.stoppedBackupCntrs {
		tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
			All:     true,
			Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backupName + "=" + name}),
		}).Return([]types.Container{cntr}, nil).Maybe()

		if slices.IndexFunc(liveBackupers, filterLabelVal(tm.mngr.labels.backuperName, name)) == -1 &&
			slices.IndexFunc(stoppedBackupers, filterLabelVal(tm.mngr.labels.backuperName, name)) == -1 {
			tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
				Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backuperName + "=" + name}),
			}).Return([]types.Container{}, nil).Maybe()

			tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
				All:     true,
				Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backuperName + "=" + name}),
			}).Return([]types.Container{}, nil).Maybe()
		}
	}

	for name, cntr := range tm.liveBackupers {
		tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
			Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backuperName + "=" + name}),
		}).Return([]types.Container{cntr}, nil).Maybe()

		tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
			All:     true,
			Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backuperName + "=" + name}),
		}).Return([]types.Container{cntr}, nil).Maybe()

		if slices.IndexFunc(liveBackupCntrs, filterLabelVal(tm.mngr.labels.backupName, name)) == -1 &&
			slices.IndexFunc(stoppedBackupCntrs, filterLabelVal(tm.mngr.labels.backupName, name)) == -1 {
			tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
				Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backupName + "=" + name}),
			}).Return([]types.Container{}, nil).Maybe()

			tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
				All:     true,
				Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backupName + "=" + name}),
			}).Return([]types.Container{}, nil).Maybe()
		}
	}

	for name, cntr := range tm.stoppedBackupers {
		tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
			All:     true,
			Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backuperName + "=" + name}),
		}).Return([]types.Container{cntr}, nil).Maybe()

		if slices.IndexFunc(liveBackupCntrs, filterLabelVal(tm.mngr.labels.backupName, name)) == -1 &&
			slices.IndexFunc(stoppedBackupCntrs, filterLabelVal(tm.mngr.labels.backupName, name)) == -1 {
			tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
				Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backupName + "=" + name}),
			}).Return([]types.Container{}, nil).Maybe()

			tm.docker.EXPECT().ContainerList(mock.Anything, container.ListOptions{
				All:     true,
				Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: tm.mngr.labels.backupName + "=" + name}),
			}).Return([]types.Container{}, nil).Maybe()
		}
	}
}

func (tm *testMngr) expectImageList() {
	imgs := []string{}
	for _, tmpl := range []*Template{tm.mngr.tmpls.Backuper, tm.mngr.tmpls.ForceBackup, tm.mngr.tmpls.Restore} {
		if len(tmpl.Image) > 0 && slices.Index(imgs, tmpl.Image) == -1 {
			img := tmpl.Image
			if !strings.Contains(img, ":") {
				img += ":latest"
			}
			imgs = append(imgs, tmpl.Image)
		}
	}

	tm.docker.EXPECT().ImageList(mock.Anything, mock.Anything).Return([]image.Summary{{RepoTags: imgs}}, nil)
}

func (tm *testMngr) expectListenEvents() {
	tm.eventsChan = make(chan events.Message)
	tm.errChan = make(chan error)

	tm.docker.EXPECT().Events(mock.Anything, mock.Anything).Return(tm.eventsChan, tm.errChan)
}

// TODo pass name to set label on backuper
func (tm *testMngr) expectCreateAndStart(t *testing.T, tmpl *Template, tag string) {
	_, cntrCfg, hstCfg, netCfg, err := tmpl.CreateConfig(tag)
	require.NoError(t, err)

	img := cntrCfg.Image
	if strings.Contains(img, ":") {
		img += ":latest"
	}

	tm.docker.EXPECT().ImageList(mock.Anything, mock.Anything).Return([]image.Summary{{RepoTags: []string{img}}}, nil)
	tm.docker.EXPECT().ContainerCreate(mock.Anything, cntrCfg, hstCfg, netCfg, nil, nil).Return(container.CreateResponse{ID: "hello"}, nil)
	tm.docker.EXPECT().ContainerStart(mock.Anything, mock.Anything, mock.Anything).Return(nil)
}

// TODO: test pull fail in separate test, no ezpectpullfail func
func (tm *testMngr) expectPullCreateAndStart(t *testing.T, tmpl *Template, tag string) {
	_, cntrCfg, hstCfg, netCfg, err := tmpl.CreateConfig(tag)
	require.NoError(t, err)

	img := cntrCfg.Image
	if strings.Contains(img, ":") {
		img += ":latest"
	}

	tm.docker.EXPECT().ImageList(mock.Anything, mock.Anything).Return([]image.Summary{}, nil)

	resp := strings.NewReader("")
	tm.docker.EXPECT().ImagePull(mock.Anything, img, mock.Anything).Return(io.NopCloser(resp), nil)

	tm.docker.EXPECT().ContainerCreate(mock.Anything, cntrCfg, hstCfg, netCfg, nil, nil).Return(container.CreateResponse{ID: "hello"}, nil)
	tm.docker.EXPECT().ContainerStart(mock.Anything, mock.Anything, mock.Anything).Return(nil)
}

func (tm *testMngr) expectBuildCreateAndStart(t *testing.T, tmpl *Template, tag string) {
	buildInfo, cntrCfg, hstCfg, netCfg, err := tmpl.CreateConfig(tag)
	require.NoError(t, err)
	require.NotNil(t, buildInfo)

	img := cntrCfg.Image
	if strings.Contains(img, ":") {
		img += ":latest"
	}

	tm.docker.EXPECT().ImageList(mock.Anything, mock.Anything).Return([]image.Summary{}, nil)

	opts := types.ImageBuildOptions{
		Tags: []string{img},
	}

	if len(buildInfo.Data.Dockerfile) > 0 {
		opts.Dockerfile = buildInfo.Data.Dockerfile
	}

	resp := strings.NewReader("")
	tm.docker.EXPECT().ImageBuild(mock.Anything, mock.Anything, opts).Return(types.ImageBuildResponse{Body: io.NopCloser(resp)}, nil)

	tm.docker.EXPECT().ContainerCreate(mock.Anything, cntrCfg, hstCfg, netCfg, nil, nil).Return(container.CreateResponse{ID: "hello"}, nil)
	tm.docker.EXPECT().ContainerStart(mock.Anything, mock.Anything, mock.Anything).Return(nil)
}

func newEmptyTestMngr(t *testing.T) testMngr {
	return newTestMngr(t, nil, nil, nil)
}

func TestExample(t *testing.T) {
	tm := newTestMngr(t, []string{"example"}, nil, nil)

	tm.expectCntrList()

	ctx, cancel := context.WithCancel(context.Background())

	tm.docker.EXPECT().Events(mock.Anything, mock.Anything).Return(make(<-chan events.Message), make(<-chan error))

	tm.docker.EXPECT().ImageList(mock.Anything, mock.Anything).Return([]image.Summary{{RepoTags: []string{"alpine:latest"}}}, nil)

	tm.docker.EXPECT().ContainerCreate(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(container.CreateResponse{ID: "hello"}, nil)
	tm.docker.EXPECT().ContainerStart(mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// docker.EXPECT().ContainerList(mock.Anything, mock.Anything).Run(func(ctx context.Context, options container.ListOptions) {
	// 	fmt.Println("fallback called")
	// }).Return([]types.Container{}, nil)

	go func() {
		require.NoError(t, tm.mngr.Run(ctx))
	}()

	<-time.After(3 * time.Second)

	cancel()

	// for _, c := range tm.docker.Calls {
	// 	fmt.Println(c)
	// }
}
