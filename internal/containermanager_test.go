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

	tm.expectBackuperCreateAndStart(t, "example")

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
	tm.expectBackuperCreateAndStart(t, "example")
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
	tm := newTestMngr(t, []string{"example"}, nil, UserTemplates{Backuper: &Template{Build: BuildInfo{Data: struct {
		Context    string
		Dockerfile string
	}{Context: "."}}}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tm.expectListenEvents()
	tm.expectImageList(nil)

	tm.expectBuild(tm.mngr.labels.backuperTag + ":latest")

	tm.expectBackuperCreateAndStart(t, "example")

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

	tm.expectBackuperCreateAndStart(t, "example")

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

	tm.expectBackuperCreateAndStart(t, "example")

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

	tm.expectBackuperCreateAndStart(t, "example")

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
		tm.mngr.StartRestore(ctx, "example")
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
		tm.mngr.StartRestore(ctx, "example")
	}()

	<-time.After(time.Second)

	eventsChan <- events.Message{}

	<-time.After(time.Second)
}

// TODO test env transfer, volumes, networks from labels
// test tmpl overlay
