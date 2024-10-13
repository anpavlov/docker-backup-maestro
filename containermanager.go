package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/anpavlov/docker-backup-mastro.git/backuper"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	labelPrefix          = "docker-backup-maestro"
	labelBackup          = labelPrefix + ".backup"
	labelBackupName      = labelPrefix + ".name"
	labelBackupPath      = labelPrefix + ".path"
	labelBackupEnvPrefix = labelBackup + ".env."

	labelBackuper     = labelPrefix + ".backuper"
	labelBackuperName = labelPrefix + ".backuper.name"
)

type dockerApi interface {
	Events(ctx context.Context, options events.ListOptions) (<-chan events.Message, <-chan error)
	ContainerList(ctx context.Context, options container.ListOptions) ([]types.Container, error)
	ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error)
	ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error
}

type ContainerManager struct {
	docker      dockerApi
	backuperCfg *backuper.Template
	conf        Config
	cli         *client.Client
	initDone    chan struct{}
}

func NewContainerManager(api dockerApi, userCfg *backuper.Template, conf Config) *ContainerManager {
	return &ContainerManager{
		docker:      api,
		conf:        conf,
		backuperCfg: userCfg,
		initDone:    make(chan struct{}),
	}
}

func (mngr *ContainerManager) Run(ctx context.Context) error {
	errChan := make(chan error)
	go func() {
		defer close(errChan)
		errChan <- mngr.listenEvents(ctx)
	}()

	select {
	case <-ctx.Done():
		return nil
	default:
	}

	mngr.initContainerList(ctx)

	select {
	case <-ctx.Done():
		return nil
	default:
	}

	close(mngr.initDone)

	return <-errChan
}

// мы хотим получить список контейнеров, ДЛЯ которых надо делать бекапные контейнеры,
// а также список самих бекапных контейнеров, чтобы проверить, что все нужные есть, а ненужные выкосить
func (mngr *ContainerManager) initContainerList(ctx context.Context) error {
	backupers, err := mngr.listContainersWithLabel(ctx, labelBackuper)
	if err != nil {
		return err
	}

	toBackups, err := mngr.listContainersWithLabel(ctx, labelBackup)
	if err != nil {
		return err
	}

	for _, backuper := range backupers {
		backupName := backuper.Labels[labelBackuperName]
		found := false

		for _, toBackup := range toBackups {
			if toBackup.Labels[labelBackupName] == backupName {
				found = true
				break
			}
		}

		if !found {
			err := mngr.dropBackuper(ctx, backupName)
			if err != nil {
				return err
			}
		}
	}

	for _, toBackup := range toBackups {
		backupName := toBackup.Labels[labelBackupName]
		found := false

		for _, backuper := range backupers {
			if backuper.Labels[labelBackuperName] == backupName {
				found = true
				mngr.updateBackuper(ctx, toBackup, backuper)
				break
			}
		}

		if !found {
			err := mngr.createBackuper(ctx, backupName)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (mngr *ContainerManager) dropBackuper(ctx context.Context, name string) error {
	log.Println("drop backuper")
	return nil
}

func (mngr *ContainerManager) createBackuper(ctx context.Context, name string) error {
	log.Println("create backuper")

	backuperCfg, err := mngr.prepareBackuperConfigFor(ctx, name)
	if err != nil {
		return err
	}

	cntrCfg, hstCfg, netCfg := backuperCfg.CreateConfig()
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

func (mngr *ContainerManager) updateBackuper(ctx context.Context, toBackup, backuper types.Container) error {
	log.Println("sync backuper")
	return nil
}

func (mngr *ContainerManager) prepareBackuperConfigFor(ctx context.Context, name string) (*backuper.Template, error) {
	cntr, err := mngr.getBackupContainerByName(ctx, name)
	if err != nil {
		return nil, err
	}

	backuperCfg := &backuper.Template{
		Labels: map[string]string{
			labelBackuperName: name,
		},
		Env:   map[string]string{},
		Binds: map[string]string{},
	}

	hostPathToBind := getContainerLabel(cntr, labelBackupPath)
	if len(hostPathToBind) == 0 {
		return nil, fmt.Errorf("could not find path to mount for backup")
	}

	backuperCfg.Binds[hostPathToBind] = mngr.conf.Backuper.BindToPath
	log.Printf("backuper bind %s to %s\n", hostPathToBind, mngr.conf.Backuper.BindToPath)

	for label, value := range cntr.Labels {
		if strings.HasPrefix(label, labelBackupEnvPrefix) {
			envName, _ := strings.CutPrefix(label, labelBackupEnvPrefix)
			backuperCfg.Env[envName] = value
			log.Printf("Env %s = %s\n", envName, value)
		}
	}

	// TODO получать из backup контейнера по лейблам путь на хосте (или имя volume), который будем маппить внутрь backuper'а
	// а также еще какие-то параметры
	// параметры, как это маппить в backuper - задается конфигом backup-maestro через env. или темплейтим в юзер конфиг?
	// МОЖНО ПРОКИДЫВАТЬ ЛЮБЫЕ ЭНВЫ по префиксу lable'а backup-maestro.backuper.env.<NAME>

	backuperCfg.Overlay(mngr.backuperCfg)

	return backuperCfg, nil
}
