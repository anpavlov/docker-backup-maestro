package internal

import (
	"context"
	"fmt"
	"io"
	"log"
	"path"
	"regexp"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type labels struct {
	backupName      string
	backupPath      string
	backupNetworks  string
	backupVolume    string
	backupEnvPrefix string

	backuperName            string
	backuperConsistencyHash string
	forceBackup             string
	restore                 string
}

func prepareLabels(prefix string) labels {
	backup := prefix + ".backup"
	return labels{
		backupName:      backup + ".name",
		backupPath:      backup + ".path",
		backupNetworks:  backup + ".networks",
		backupVolume:    backup + ".volume",
		backupEnvPrefix: backup + ".env.",

		backuperName:            prefix + ".backuper" + ".name",
		backuperConsistencyHash: prefix + ".backuper" + ".consistencyhash",

		forceBackup: prefix + ".forcebackup",
		restore:     prefix + ".restore",
	}
}

type dockerApi interface {
	Events(ctx context.Context, options events.ListOptions) (<-chan events.Message, <-chan error)
	ContainerList(ctx context.Context, options container.ListOptions) ([]types.Container, error)
	ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error)
	ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error
	ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error
	ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error
	ImageBuild(ctx context.Context, buildContext io.Reader, options types.ImageBuildOptions) (types.ImageBuildResponse, error)
	ImageList(ctx context.Context, options image.ListOptions) ([]image.Summary, error)
	ImagePull(ctx context.Context, refStr string, options image.PullOptions) (io.ReadCloser, error)
}

type UserTemplates struct {
	Backuper    *Template
	Restore     *Template
	ForceBackup *Template
}

type ContainerManager struct {
	docker dockerApi
	tmpls  UserTemplates
	conf   Config
	labels labels
}

func NewContainerManager(api dockerApi, userCfg UserTemplates, conf Config) *ContainerManager {
	return &ContainerManager{
		docker: api,
		conf:   conf,
		tmpls:  userCfg,
		labels: prepareLabels(conf.LabelPrefix),
	}
}

func (mngr *ContainerManager) Run(ctx context.Context) error {
	return mngr.syncBackupers(ctx)
}

func (mngr *ContainerManager) initBackupers(ctx context.Context) error {
	backupers, err := mngr.listContainersWithLabel(ctx, mngr.labels.backuperName, true)
	if err != nil {
		return err
	}

	toBackups, err := mngr.listContainersWithLabel(ctx, mngr.labels.backupName, false)
	if err != nil {
		return err
	}

	for _, backuper := range backupers {
		backupName := backuper.Labels[mngr.labels.backuperName]
		found := false

		for _, toBackup := range toBackups {
			if toBackup.Labels[mngr.labels.backupName] == backupName {
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
		backupName := toBackup.Labels[mngr.labels.backupName]
		found := false

		for _, backuper := range backupers {
			if backuper.Labels[mngr.labels.backuperName] == backupName {
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
	log.Println("drop backuper", name)

	cntr, err := mngr.getContainerByLabelValue(ctx, mngr.labels.backuperName, name, false)
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
	log.Println("create backuper", name)

	alphanumeric := regexp.MustCompile("^[a-zA-Z0-9-._]*$")
	if !alphanumeric.MatchString(name) {
		log.Printf("ERROR: invalid backup name: %s\n", name)
		log.Println("ERROR: backup name must contain only letters, digits and '-' '_' '.'")
		return nil
	}

	existingBackuper, err := mngr.getContainerByLabelValue(ctx, mngr.labels.backuperName, name, true)
	if err != nil {
		return err
	}

	if existingBackuper != nil {
		existingBackup, err := mngr.getContainerByLabelValue(ctx, mngr.labels.backupName, name, true)
		if err != nil {
			return err
		}

		return mngr.updateBackuper(ctx, *existingBackup, *existingBackuper)
	}

	backuperCfg, err := mngr.prepareBackuperConfigFor(ctx, name, false)
	if err != nil {
		return err
	}

	backuperCfg = mngr.tmpls.Backuper.Overlay(backuperCfg)

	hash := backuperCfg.Hash()

	backuperCfg.Labels[mngr.labels.backuperConsistencyHash] = hash

	cntrName := strings.ReplaceAll(mngr.conf.BackupNameFormat, "{name}", name)

	return mngr.startBackuper(ctx, backuperCfg, cntrName)
}

func (mngr *ContainerManager) updateBackuper(ctx context.Context, toBackup, backuper types.Container) error {
	backupName := toBackup.Labels[mngr.labels.backupName]

	log.Println("sync backuper", backupName)

	backuperCfg, err := mngr.prepareBackuperConfigFor(ctx, backupName, false)
	if err != nil {
		return err
	}

	backuperCfg = mngr.tmpls.Backuper.Overlay(backuperCfg)

	hash := backuperCfg.Hash()

	backuperHash := backuper.Labels[mngr.labels.backuperConsistencyHash]

	if hash == backuperHash {
		log.Println("no need to recreate", backupName)
		return nil
	}

	err = mngr.dropBackuper(ctx, backupName)
	if err != nil {
		return fmt.Errorf("failed to drop backuper %s: %w", backupName, err)
	}

	return mngr.createBackuper(ctx, backupName)
}

func (mngr *ContainerManager) prepareBackuperConfigFor(ctx context.Context, name string, rw bool) (*Template, error) {
	cntr, err := mngr.getContainerByLabelValue(ctx, mngr.labels.backupName, name, true)
	if err != nil {
		return nil, err
	}

	if cntr == nil {
		return nil, fmt.Errorf("backup container '%s' not found", name)
	}

	backuperBaseCfg := &Template{
		Labels: map[string]string{
			mngr.labels.backuperName: name,
		},
	}

	volumes := []string{}

	// check for multipath first
	for label, value := range cntr.Labels {
		if strings.HasPrefix(label, mngr.labels.backupPath+".") {
			dirName := strings.TrimPrefix(label, mngr.labels.backupPath+".")
			hostPath := value

			bind := fmt.Sprintf("%s:%s", hostPath, path.Join(mngr.conf.Backuper.BindToPath, dirName))

			if !mngr.conf.AlwaysRw && !rw {
				bind += ":ro"
			}

			volumes = append(volumes, bind)
		}
	}

	if len(volumes) == 0 {
		hostPathToBind := getContainerLabel(cntr, mngr.labels.backupPath)
		if len(hostPathToBind) != 0 {
			bind := fmt.Sprintf("%s:%s", hostPathToBind, mngr.conf.Backuper.BindToPath)
			if !rw {
				bind += ":ro"
			}

			volumes = append(volumes, bind)
		}
	}

	// check for additional volumes
	for label, value := range cntr.Labels {
		if strings.HasPrefix(label, mngr.labels.backupVolume) {
			volumes = append(volumes, value)
		}
	}

	backuperBaseCfg.Volumes = volumes

	for label, value := range cntr.Labels {
		if strings.HasPrefix(label, mngr.labels.backupEnvPrefix) {
			envName, _ := strings.CutPrefix(label, mngr.labels.backupEnvPrefix)

			if backuperBaseCfg.Environment == nil {
				backuperBaseCfg.Environment = make(StringMapOrArray)
			}

			backuperBaseCfg.Environment[envName] = value
		}
	}

	networksLabel := getContainerLabel(cntr, mngr.labels.backupNetworks)
	if len(networksLabel) > 0 {
		nets := strings.Split(networksLabel, ",")
		backuperBaseCfg.Networks = nets
	}

	return backuperBaseCfg, nil
}

func (mngr *ContainerManager) oneOffContainerFromTmpl(ctx context.Context, name string, tmpl *Template, tag string, cntrNameFormat string) error {
	backuperCntr, err := mngr.getContainerByLabelValue(ctx, mngr.labels.backuperName, name, false)
	if err != nil {
		return err
	}

	wasRunning := containerIsAlive(backuperCntr)

	if backuperCntr != nil {
		log.Printf("stopping backup container %s\n", name)
		err = mngr.docker.ContainerStop(ctx, backuperCntr.ID, container.StopOptions{})
		if err != nil {
			return fmt.Errorf("failed to stop backuper container %s %s - %w", name, backuperCntr.ID, err)
		}
	}

	oneOffCfg, err := mngr.prepareBackuperConfigFor(ctx, name, true)
	if err != nil {
		return fmt.Errorf("failed to generate config for %s - %w", name, err)
	}

	delete(oneOffCfg.Labels, mngr.labels.backuperName)
	oneOffCfg.Labels[tag] = name

	oneOffCfg = tmpl.Overlay(oneOffCfg)

	oneOffCfg.autoRemove = true

	cntrName := strings.ReplaceAll(cntrNameFormat, "{name}", name)

	cntrId, err := mngr.createContainer(ctx, oneOffCfg, tag, cntrName)
	if err != nil {
		return err
	}

	errChan := make(chan error)
	go func() {
		defer close(errChan)
		errChan <- mngr.waitForStop(ctx, cntrId)
	}()

	log.Printf("starting restore container %s\n", name)
	err = mngr.docker.ContainerStart(ctx, cntrId, container.StartOptions{})
	if err != nil {
		return err
	}

	log.Printf("wainting restore container %s to finish\n", name)
	err = <-errChan
	if err != nil {
		return err
	}

	if wasRunning {
		log.Printf("starting backup container %s\n", name)
		err = mngr.docker.ContainerStart(ctx, backuperCntr.ID, container.StartOptions{})
		if err != nil {
			return fmt.Errorf("failed to start backuper %s - %w", name, err)
		}
	}

	return nil
}

func (mngr *ContainerManager) Restore(ctx context.Context, name string) error {
	if mngr.tmpls.Restore == nil {
		return fmt.Errorf("restore template not set")
	}

	return mngr.oneOffContainerFromTmpl(ctx, name, mngr.tmpls.Restore, mngr.conf.RestoreTag, mngr.conf.RestoreNameFormat)
}

func (mngr *ContainerManager) RestoreAll(ctx context.Context) error {
	if mngr.tmpls.Restore == nil {
		return fmt.Errorf("restore template not set")
	}

	toBackups, err := mngr.listContainersWithLabel(ctx, mngr.labels.backupName, true)
	if err != nil {
		return err
	}

	for _, backupCntr := range toBackups {
		backupName := backupCntr.Labels[mngr.labels.backupName]
		log.Printf("Restoring %s\n", backupName)

		err := mngr.oneOffContainerFromTmpl(ctx, backupName, mngr.tmpls.Restore, mngr.conf.RestoreTag, mngr.conf.RestoreNameFormat)
		if err != nil {
			return err
		}
	}

	return nil
}

func (mngr *ContainerManager) ForceBackup(ctx context.Context, name string) error {
	if mngr.tmpls.ForceBackup == nil {
		return fmt.Errorf("force backup template not set")
	}

	return mngr.oneOffContainerFromTmpl(ctx, name, mngr.tmpls.ForceBackup, mngr.conf.ForceTag, mngr.conf.ForceNameFormat)
}

func (mngr *ContainerManager) ForceBackupAll(ctx context.Context, includeStopped bool) error {
	if mngr.tmpls.ForceBackup == nil {
		return fmt.Errorf("force backup template not set")
	}

	toBackups, err := mngr.listContainersWithLabel(ctx, mngr.labels.backupName, includeStopped)
	if err != nil {
		return err
	}

	for _, backupCntr := range toBackups {
		backupName := backupCntr.Labels[mngr.labels.backupName]
		log.Printf("Running force backup %s\n", backupName)

		err := mngr.oneOffContainerFromTmpl(ctx, backupName, mngr.tmpls.ForceBackup, mngr.conf.ForceTag, mngr.conf.ForceNameFormat)
		if err != nil {
			return err
		}
	}

	return nil
}

func (mngr *ContainerManager) BuildAll(ctx context.Context) error {
	for tag, tmpl := range map[string]*Template{
		mngr.conf.BackupTag:  mngr.tmpls.Backuper,
		mngr.conf.ForceTag:   mngr.tmpls.ForceBackup,
		mngr.conf.RestoreTag: mngr.tmpls.Restore,
	} {
		bInfo, cntrCfg, _, _, err := tmpl.CreateConfig(tag)
		if err != nil {
			return err
		}

		if bInfo != nil {
			log.Printf("Building %s\n", cntrCfg.Image)

			err = mngr.buildImage(ctx, bInfo, cntrCfg.Image, true)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (mngr *ContainerManager) BuildBackuper(ctx context.Context) error {
	bInfo, cntrCfg, _, _, err := mngr.tmpls.Backuper.CreateConfig(mngr.conf.BackupTag)
	if err != nil {
		return err
	}

	if bInfo != nil {
		log.Printf("Building %s\n", cntrCfg.Image)

		err = mngr.buildImage(ctx, bInfo, cntrCfg.Image, true)
		if err != nil {
			return err
		}
	}

	return nil
}

func (mngr *ContainerManager) BuildRestore(ctx context.Context) error {
	bInfo, cntrCfg, _, _, err := mngr.tmpls.Restore.CreateConfig(mngr.conf.RestoreTag)
	if err != nil {
		return err
	}

	if bInfo != nil {
		log.Printf("Building %s\n", cntrCfg.Image)

		err = mngr.buildImage(ctx, bInfo, cntrCfg.Image, true)
		if err != nil {
			return err
		}
	}

	return nil
}

func (mngr *ContainerManager) BuildForce(ctx context.Context) error {
	bInfo, cntrCfg, _, _, err := mngr.tmpls.ForceBackup.CreateConfig(mngr.conf.ForceTag)
	if err != nil {
		return err
	}

	if bInfo != nil {
		log.Printf("Building %s\n", cntrCfg.Image)

		err = mngr.buildImage(ctx, bInfo, cntrCfg.Image, true)
		if err != nil {
			return err
		}
	}

	return nil
}

func (mngr *ContainerManager) StopBackuper(ctx context.Context, name string) error {
	cntr, err := mngr.getContainerByLabelValue(ctx, mngr.labels.backuperName, name, false)
	if err != nil {
		return err
	}

	if cntr == nil {
		return fmt.Errorf("backup container '%s' is stopped or doesn't exist", name)
	}

	log.Printf("Stopping '%s' backup container\n", name)

	return mngr.docker.ContainerStop(ctx, cntr.ID, container.StopOptions{})
}

func (mngr *ContainerManager) StopAll(ctx context.Context) error {
	backupers, err := mngr.listContainersWithLabel(ctx, mngr.labels.backuperName, false)
	if err != nil {
		return err
	}

	for _, backuper := range backupers {
		log.Printf("Stopping '%s' backup container\n", backuper.Labels[mngr.labels.backuperName])

		err := mngr.docker.ContainerStop(ctx, backuper.ID, container.StopOptions{})
		if err != nil {
			return err
		}
	}

	return nil
}

func (mngr *ContainerManager) RemoveBackuper(ctx context.Context, name string) error {
	cntr, err := mngr.getContainerByLabelValue(ctx, mngr.labels.backuperName, name, true)
	if err != nil {
		return err
	}

	if cntr == nil {
		return fmt.Errorf("backup container '%s' doesn't exist", name)
	}

	err = mngr.docker.ContainerStop(ctx, cntr.ID, container.StopOptions{})
	if err != nil {
		return err
	}

	log.Printf("Removing '%s' backup container\n", name)

	return mngr.docker.ContainerRemove(ctx, cntr.ID, container.RemoveOptions{})
}

func (mngr *ContainerManager) RemoveAll(ctx context.Context) error {
	backupers, err := mngr.listContainersWithLabel(ctx, mngr.labels.backuperName, true)
	if err != nil {
		return err
	}

	for _, backuper := range backupers {
		err = mngr.docker.ContainerStop(ctx, backuper.ID, container.StopOptions{})
		if err != nil {
			return err
		}

		log.Printf("Removing '%s' backup container\n", backuper.Labels[mngr.labels.backuperName])

		err = mngr.docker.ContainerRemove(ctx, backuper.ID, container.RemoveOptions{})
		if err != nil {
			return err
		}
	}

	return nil
}

func (mngr *ContainerManager) StartBackuper(ctx context.Context, name string) error {
	cntr, err := mngr.getContainerByLabelValue(ctx, mngr.labels.backuperName, name, true)
	if err != nil {
		return err
	}

	if cntr == nil {
		return fmt.Errorf("backup container '%s' doesn't exist", name)
	}

	log.Printf("Starting '%s' backup container\n", name)

	return mngr.docker.ContainerStart(ctx, cntr.ID, container.StartOptions{})
}

func (mngr *ContainerManager) StartAll(ctx context.Context) error {
	backupers, err := mngr.listContainersWithLabel(ctx, mngr.labels.backuperName, true)
	if err != nil {
		return err
	}

	for _, backuper := range backupers {
		log.Printf("Starting '%s' backup container\n", backuper.Labels[mngr.labels.backuperName])

		err := mngr.docker.ContainerStart(ctx, backuper.ID, container.StartOptions{})
		if err != nil {
			return err
		}
	}

	return nil
}

func (mngr *ContainerManager) CreateBackuper(ctx context.Context, name string) error {
	backuper, err := mngr.getContainerByLabelValue(ctx, mngr.labels.backuperName, name, true)
	if err != nil {
		return err
	}

	if backuper != nil {
		return fmt.Errorf("backup container '%s' already exists, if you want to recreate it, remove first", name)
	}

	backupCntr, err := mngr.getContainerByLabelValue(ctx, mngr.labels.backupName, name, true)
	if err != nil {
		return err
	}

	if backupCntr == nil {
		return fmt.Errorf("no container '%s' to backup", name)
	}

	return mngr.createBackuper(ctx, name)
}

func (mngr *ContainerManager) CreateAll(ctx context.Context) error {
	backupCntrs, err := mngr.listContainersWithLabel(ctx, mngr.labels.backupName, true)
	if err != nil {
		return err
	}

	for _, backupCntr := range backupCntrs {
		name := backupCntr.Labels[mngr.labels.backupName]

		backuper, err := mngr.getContainerByLabelValue(ctx, mngr.labels.backuperName, name, true)
		if err != nil {
			return err
		}

		if backuper != nil {
			log.Printf("backup container '%s' already exists, skipping\n", name)
			continue
		}

		err = mngr.createBackuper(ctx, name)
		if err != nil {
			return err
		}
	}

	return nil
}

func (mngr *ContainerManager) PullBackuper(ctx context.Context) error {
	if len(mngr.tmpls.Backuper.Image) == 0 {
		return fmt.Errorf("no image in template")
	}

	return mngr.pullImage(ctx, mngr.tmpls.Backuper.Image, true)
}

func (mngr *ContainerManager) PullRestore(ctx context.Context) error {
	if len(mngr.tmpls.Restore.Image) == 0 {
		return fmt.Errorf("no image in template")
	}

	return mngr.pullImage(ctx, mngr.tmpls.Restore.Image, true)
}

func (mngr *ContainerManager) PullForce(ctx context.Context) error {
	if len(mngr.tmpls.ForceBackup.Image) == 0 {
		return fmt.Errorf("no image in template")
	}

	return mngr.pullImage(ctx, mngr.tmpls.ForceBackup.Image, true)
}

func (mngr *ContainerManager) PullAll(ctx context.Context) error {
	for _, tmpl := range []*Template{mngr.tmpls.Backuper, mngr.tmpls.ForceBackup, mngr.tmpls.Restore} {
		if len(tmpl.Image) == 0 {
			continue
		}

		err := mngr.pullImage(ctx, mngr.tmpls.Backuper.Image, true)
		if err != nil {
			return err
		}
	}

	return nil
}

type ListOptions struct {
	All          bool
	Backupers    bool
	Restores     bool
	ForceBackups bool
}

func (mngr *ContainerManager) List(ctx context.Context, opts ListOptions) error {
	label := mngr.labels.backupName

	if opts.Backupers {
		label = mngr.labels.backuperName
	}

	if opts.Restores {
		label = mngr.labels.restore
	}

	if opts.ForceBackups {
		label = mngr.labels.forceBackup
	}

	cntrs, err := mngr.listContainersWithLabel(ctx, label, opts.All)
	if err != nil {
		return err
	}

	names := []string{}

	for _, cntr := range cntrs {
		name := cntr.Labels[label]

		if len(name) == 0 {
			return fmt.Errorf("failed to get container name, label %s report to maintainer", label)
		}

		names = append(names, name)

	}

	for _, name := range names {
		fmt.Println(name)
	}

	return nil
}
