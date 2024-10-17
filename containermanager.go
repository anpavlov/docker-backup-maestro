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
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	labelPrefix                = "docker-backup-maestro"
	labelBackup                = labelPrefix + ".backup"
	labelBackupName            = labelBackup + ".name"
	labelBackupPath            = labelBackup + ".path"
	labelBackupNetwork         = labelBackup + ".network"
	labelBackupEnvPrefix       = labelBackup + ".env."
	labelBackupConsistencyHash = labelBackup + ".consistencyhash"

	labelBackuper     = labelPrefix + ".backuper"
	labelBackuperName = labelBackuper + ".name"
)

type dockerApi interface {
	Events(ctx context.Context, options events.ListOptions) (<-chan events.Message, <-chan error)
	ContainerList(ctx context.Context, options container.ListOptions) ([]types.Container, error)
	ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error)
	ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error
	ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error
	ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error
}

type UserTemplates struct {
	Backuper    *backuper.Template
	Restore     *backuper.Template
	ForceBackup *backuper.Template
}

type ContainerManager struct {
	docker dockerApi
	tmpls  UserTemplates
	conf   Config
	// cli      *client.Client
	initDone chan struct{}
}

func NewContainerManager(api dockerApi, userCfg UserTemplates, conf Config) *ContainerManager {
	return &ContainerManager{
		docker:   api,
		conf:     conf,
		tmpls:    userCfg,
		initDone: make(chan struct{}),
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
	backupers, err := mngr.listContainersWithLabel(ctx, labelBackuperName, true)
	if err != nil {
		return err
	}

	toBackups, err := mngr.listContainersWithLabel(ctx, labelBackupName, false)
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

	cntr, err := mngr.getContainerByLabelValue(ctx, labelBackuperName, name, false)
	if err != nil {
		return err
	}

	if cntr == nil {
		log.Printf("Backuper container for %s not found. Skipping\n", name)
		return nil
	}

	err = mngr.docker.ContainerStop(ctx, cntr.ID, container.StopOptions{})
	if err != nil {
		return err
	}

	err = mngr.docker.ContainerRemove(ctx, cntr.ID, container.RemoveOptions{})
	if err != nil {
		return err
	}

	return nil
}

func (mngr *ContainerManager) createBackuper(ctx context.Context, name string) error {
	log.Println("create backuper")

	existingBackuper, err := mngr.getContainerByLabelValue(ctx, labelBackuperName, name, true)
	if err != nil {
		return err
	}

	if existingBackuper != nil {
		existingBackup, err := mngr.getContainerByLabelValue(ctx, labelBackupName, name, true)
		if err != nil {
			return err
		}

		return mngr.updateBackuper(ctx, *existingBackup, *existingBackuper)
	}

	backuperCfg, err := mngr.prepareBackuperConfigFor(ctx, name, false)
	if err != nil {
		return err
	}

	backuperCfg.Overlay(mngr.tmpls.Backuper)

	hash := backuperCfg.Hash()

	backuperCfg.Labels[labelBackupConsistencyHash] = hash

	return mngr.startContainer(ctx, backuperCfg)
}

func (mngr *ContainerManager) updateBackuper(ctx context.Context, toBackup, backuper types.Container) error {
	log.Println("sync backuper")

	backupName := toBackup.Labels[labelBackupName]

	// Собрать backuper.Template как будто мы собираемся создавать контейнер, посчитать хэш от итогового темплейта
	// и сравнить с хэшом запущенного, который записан в служебный лейбл

	backuperCfg, err := mngr.prepareBackuperConfigFor(ctx, backupName, false)
	if err != nil {
		return err
	}

	backuperCfg.Overlay(mngr.tmpls.Backuper)

	hash := backuperCfg.Hash()

	backuperHash := backuper.Labels[labelBackupConsistencyHash]

	if hash == backuperHash {
		return nil
	}

	err = mngr.dropBackuper(ctx, backupName)
	if err != nil {
		return fmt.Errorf("failed to drop backuper %s: %w", backupName, err)
	}

	return mngr.createBackuper(ctx, backupName)
}

func (mngr *ContainerManager) prepareBackuperConfigFor(ctx context.Context, name string, rw bool) (*backuper.Template, error) {
	cntr, err := mngr.getContainerByLabelValue(ctx, labelBackupName, name, true)
	if err != nil {
		return nil, err
	}

	if cntr == nil {
		return nil, fmt.Errorf("backup container '%s' not found", name)
	}

	backuperBaseCfg := &backuper.Template{
		Labels: map[string]string{
			labelBackuperName: name,
		},
		Environment: map[string]string{},
	}

	hostPathToBind := getContainerLabel(cntr, labelBackupPath)
	if len(hostPathToBind) == 0 {
		return nil, fmt.Errorf("could not find path to mount for backup")
	}

	bind := fmt.Sprintf("%s:%s", hostPathToBind, mngr.conf.Backuper.BindToPath)
	if !rw {
		bind += ":ro"
	}

	backuperBaseCfg.Volumes = []string{bind}
	log.Printf("backuper bind %s\n", bind)

	for label, value := range cntr.Labels {
		if strings.HasPrefix(label, labelBackupEnvPrefix) {
			envName, _ := strings.CutPrefix(label, labelBackupEnvPrefix)
			backuperBaseCfg.Environment[envName] = value
			log.Printf("Env %s = %s\n", envName, value)
		}
	}

	networkLabel := getContainerLabel(cntr, labelBackupNetwork)
	if len(networkLabel) > 0 {
		backuperBaseCfg.Networks = []string{networkLabel}
	}

	// TODO получать из backup контейнера по лейблам путь на хосте (или имя volume), который будем маппить внутрь backuper'а
	// а также еще какие-то параметры
	// параметры, как это маппить в backuper - задается конфигом backup-maestro через env. или темплейтим в юзер конфиг?
	// МОЖНО ПРОКИДЫВАТЬ ЛЮБЫЕ ЭНВЫ по префиксу lable'а backup-maestro.backuper.env.<NAME>

	// backuperBaseCfg.Overlay(mngr.tmpls.Backuper)

	return backuperBaseCfg, nil
}

func (mngr *ContainerManager) StartRestore(ctx context.Context, name string) error {
	return mngr.oneShotContainerFromTmpl(ctx, name, mngr.tmpls.Restore)
}

func (mngr *ContainerManager) StartForceBackup(ctx context.Context, name string) error {
	return mngr.oneShotContainerFromTmpl(ctx, name, mngr.tmpls.ForceBackup)
}

func (mngr *ContainerManager) oneShotContainerFromTmpl(ctx context.Context, name string, tmpl *backuper.Template) error {
	backuperCntr, err := mngr.getContainerByLabelValue(ctx, labelBackupName, name, false)
	if err != nil {
		return err
	}

	wasRunning := containerIsAlive(backuperCntr)

	if backuperCntr != nil {
		err = mngr.docker.ContainerStop(ctx, backuperCntr.ID, container.StopOptions{})
		if err != nil {
			return fmt.Errorf("failed to stop backuper container %s %s - %w", name, backuperCntr.ID, err)
		}
	}

	restoreCfg, err := mngr.prepareBackuperConfigFor(ctx, name, true)
	if err != nil {
		return fmt.Errorf("failed to generate config for %s - %w", name, err)
	}

	restoreCfg.Overlay(tmpl)

	cntrCfg, hstCfg, netCfg, err := restoreCfg.CreateConfig()
	if err != nil {
		return err
	}

	hstCfg.AutoRemove = true

	resp, err := mngr.docker.ContainerCreate(ctx, cntrCfg, hstCfg, netCfg, nil, "")
	if err != nil {
		return err
	}

	for _, warn := range resp.Warnings {
		log.Println("WARN:", warn)
	}

	cntrId := resp.ID

	errChan := make(chan error)
	go func() {
		defer close(errChan)
		errChan <- mngr.waitForStop(ctx, cntrId)
	}()

	err = mngr.docker.ContainerStart(ctx, cntrId, container.StartOptions{})
	if err != nil {
		return err
	}

	err = <-errChan
	if err != nil {
		return err
	}

	if wasRunning {
		err = mngr.docker.ContainerStart(ctx, backuperCntr.ID, container.StartOptions{})
		if err != nil {
			return fmt.Errorf("failed to start backuper %s - %w", name, err)
		}
	}

	return nil
}
