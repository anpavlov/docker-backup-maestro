package internal

import (
	"context"
	"testing"
	"time"

	"github.com/docker/docker/api/types/events"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewBackuperOnStart(t *testing.T) {
	tm := newTestMngr(t, []string{"example"}, nil, UserTemplates{Backuper: &Template{Image: "alpine"}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tm.expectListenEvents()
	tm.expectImageList([]string{"alpine:latest"})

	tm.expectBackuperCreateAndStart(t, "example", nil, nil)

	go func() {
		require.NoError(t, tm.mngr.Run(ctx))
	}()

	<-time.After(time.Second)
}

func TestNewBackupOnline(t *testing.T) {
	tm := newTestMngr(t, nil, nil, UserTemplates{Backuper: &Template{Image: "alpine"}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tm.expectListenEvents()

	go func() {
		require.NoError(t, tm.mngr.Run(ctx))
	}()

	<-time.After(time.Second)

	tm.expectImageList([]string{"alpine:latest"})
	tm.expectBackuperCreateAndStart(t, "example", nil, nil)
	tm.startBackupCntr("example")

	<-time.After(time.Second)
}
func TestStoppedBackuperOnStart(t *testing.T) {
	tm := newTestMngr(t, []string{"example"}, []string{"example"}, UserTemplates{Backuper: &Template{Image: "alpine"}})

	cntr := tm.liveBackupers["example"]
	delete(tm.liveBackupers, "example")
	cntr.Status = "stopped"
	tm.stoppedBackupers["example"] = cntr

	tm.resetExpectCallList()
	tm.expectCntrList()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tm.expectListenEvents()

	go func() {
		require.NoError(t, tm.mngr.Run(ctx))
	}()

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

	tm.expectBackuperRemove("example")

	go func() {
		require.NoError(t, tm.mngr.Run(ctx))
	}()

	<-time.After(time.Second)
}

func TestDropBackuperOnline(t *testing.T) {
	tm := newTestMngr(t, []string{"example"}, []string{"example"}, UserTemplates{Backuper: &Template{Image: "alpine"}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tm.expectListenEvents()

	go func() {
		require.NoError(t, tm.mngr.Run(ctx))
	}()

	<-time.After(time.Second)

	tm.expectBackuperRemove("example")
	tm.removeBackupCntr("example")

	<-time.After(time.Second)
}

func TestBuildBackuper(t *testing.T) {
	tm := newTestMngr(t, []string{"example"}, nil, UserTemplates{Backuper: &Template{Build: BuildInfo{
		Context: ".",
	}}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tm.expectListenEvents()
	tm.expectImageList(nil)

	tm.expectBuild(tm.mngr.conf.BackupTag + ":latest")

	tm.expectBackuperCreateAndStart(t, "example", nil, nil)

	go func() {
		require.NoError(t, tm.mngr.Run(ctx))
	}()

	<-time.After(time.Second)
}

func TestNoRebuildBackuper(t *testing.T) {
	tm := newTestMngr(t, []string{"example"}, nil, UserTemplates{Backuper: &Template{Build: BuildInfo{
		Context: ".",
	}}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tm.expectListenEvents()
	tm.expectImageList([]string{tm.mngr.conf.BackupTag + ":latest"})

	tm.expectBackuperCreateAndStart(t, "example", nil, nil)

	go func() {
		require.NoError(t, tm.mngr.Run(ctx))
	}()

	<-time.After(time.Second)
}

func TestPullBackuper(t *testing.T) {
	tm := newTestMngr(t, []string{"example"}, nil, UserTemplates{Backuper: &Template{Image: "alpine"}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tm.expectListenEvents()
	tm.expectImageList(nil)

	tm.expectPull("alpine:latest")

	tm.expectBackuperCreateAndStart(t, "example", nil, nil)

	go func() {
		require.NoError(t, tm.mngr.Run(ctx))
	}()

	<-time.After(time.Second)
}

func TestRecreateBackuper(t *testing.T) {
	tm := newTestMngr(t, []string{"example"}, []string{"example"}, UserTemplates{Backuper: &Template{Image: "alpine"}})

	tm.liveBackupers["example"].Labels[tm.mngr.labels.backuperConsistencyHash] = "blah"

	tm.resetExpectCallList()
	tm.expectCntrList()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tm.expectListenEvents()

	tm.expectBackuperRemove("example")

	tm.expectImageList([]string{"alpine:latest"})

	tm.expectBackuperCreateAndStart(t, "example", nil, nil)

	go func() {
		require.NoError(t, tm.mngr.Run(ctx))
	}()

	<-time.After(time.Second)
}

func TestRestoreOnline(t *testing.T) {
	tm := newTestMngr(t, []string{"example"}, []string{"example"}, UserTemplates{
		Backuper: &Template{Image: "alpine"},
		Restore:  &Template{Image: "restore"},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tm.expectListenEvents()

	go func() {
		require.NoError(t, tm.mngr.Run(ctx))
	}()

	<-time.After(time.Second)

	tm.expectBackuperStop("example")
	tm.expectImageList([]string{"restore:latest"})
	tm.expectRestoreCreateAndStart(t, "example")

	eventsChan := make(chan events.Message)
	errChan := make(chan error)

	tm.docker.EXPECT().Events(mock.Anything, mock.Anything).Return(eventsChan, errChan).Once()

	go func() {
		tm.mngr.Restore(ctx, "example")
	}()

	<-time.After(time.Second)

	tm.expectBackuperStart("example")
	eventsChan <- events.Message{}

	<-time.After(time.Second)
}

func TestRestoreStopped(t *testing.T) {
	tm := newTestMngr(t, []string{"example"}, []string{"example"}, UserTemplates{
		Backuper: &Template{Image: "alpine"},
		Restore:  &Template{Image: "restore"},
	})

	cntr := tm.liveBackupers["example"]
	delete(tm.liveBackupers, "example")
	cntr.Status = "stopped"
	tm.stoppedBackupers["example"] = cntr

	tm.resetExpectCallList()
	tm.expectCntrList()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tm.expectListenEvents()

	go func() {
		require.NoError(t, tm.mngr.Run(ctx))
	}()

	<-time.After(time.Second)

	tm.expectImageList([]string{"restore:latest"})
	tm.expectRestoreCreateAndStart(t, "example")

	eventsChan := make(chan events.Message)
	errChan := make(chan error)

	tm.docker.EXPECT().Events(mock.Anything, mock.Anything).Return(eventsChan, errChan).Once()

	go func() {
		tm.mngr.Restore(ctx, "example")
	}()

	<-time.After(time.Second)

	eventsChan <- events.Message{}

	<-time.After(time.Second)
}

func TestNewBackuperUseLabels(t *testing.T) {
	tm := newTestMngr(t, []string{"example"}, nil, UserTemplates{Backuper: &Template{Image: "alpine"}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tm.expectListenEvents()
	tm.expectImageList([]string{"alpine:latest"})

	customLabels := map[string]string{
		tm.mngr.labels.backupName:                "example",
		tm.mngr.labels.backupPath:                "/host/path",
		tm.mngr.labels.backupEnvPrefix + "MYENV": "env_val",
		tm.mngr.labels.backupNetworks:            "example_net,net_two",
		tm.mngr.labels.backupVolume:              "/host/path2:/inside2",
	}

	overlay := &Template{
		Labels:      map[string]string{tm.mngr.labels.backuperName: "example"},
		Volumes:     []string{"/host/path:/data:ro", "/host/path2:/inside2"},
		Environment: map[string]string{"MYENV": "env_val"},
		Networks:    []string{"example_net", "net_two"},
	}

	tm.expectBackuperCreateAndStart(t, "example", customLabels, overlay)

	go func() {
		require.NoError(t, tm.mngr.Run(ctx))
	}()

	<-time.After(time.Second)
}

func TestNewBackuperLabelsMultipath(t *testing.T) {
	tm := newTestMngr(t, []string{"example"}, nil, UserTemplates{Backuper: &Template{Image: "alpine"}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tm.expectListenEvents()
	tm.expectImageList([]string{"alpine:latest"})

	customLabels := map[string]string{
		tm.mngr.labels.backupName:           "example",
		tm.mngr.labels.backupPath + ".dir1": "/host/path1",
		tm.mngr.labels.backupPath + ".dir2": "/host/path2",
		tm.mngr.labels.backupPath:           "/host/path3", // should be ignored
		tm.mngr.labels.backupVolume:         "/host/path4:/inside",
		tm.mngr.labels.backupVolume + ".1":  "/host/path5:/inside2",
	}

	overlay := &Template{
		Labels:  map[string]string{tm.mngr.labels.backuperName: "example"},
		Volumes: []string{"/host/path1:/data/dir1:ro", "/host/path2:/data/dir2:ro", "/host/path4:/inside", "/host/path5:/inside2"},
	}

	tm.expectBackuperCreateAndStart(t, "example", customLabels, overlay)

	go func() {
		require.NoError(t, tm.mngr.Run(ctx))
	}()

	<-time.After(time.Second)
}

// test build/pull fail on err log
