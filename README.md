# docker-backup-maestro

Automatically start stop companion containers to backup data from another containers. May be used to auto start any companion containers, not only for backup.

## How does it work

docker-backup-maestro (maestro) is watching containers with specific labels. When any of them is created or removed, maestro will create and run or stop and delete companion container, using container template (compose-like) and labels from target container.

docker-backup-maestro provides **restore** and **force-backup** cli commands (used with docker exec) that runs separate one-off containers with autoremove flag from their own templates.

Typical setup assumes cron-based backup container, that runs continuously, starting backup procedure periodically.

CLI commands includes some useful commands that simplifies backup containers management. Like build, pull, start, stop, create, remove, list backup containers.

Note that since docker-backup-maestro runs backup container at the time target container to backup is created (even if it not run afterwards), backups will be made even if target container is never started before. This on the contrary allows to restore container data before it is run for the first time, when restoring from scratch.

Also companion backup container is totally removed when target container is removed, so no logs will be left also.

## Usage with docker compose

docker-backup-maestro container

```yml
services:

  maestro:
    image: ghcr.io/anpavlov/docker-backup-maestro:latest
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro

      # Compose-like template for backup containers. Reference below
      - ./backup.yml:/root/backup_tmpl.yml
    environment:
      # Where data will be mounted from app containers for backup
      BIND_PATH: /backup
```

Setup target container to backup with labels

```yml
services:

  app-to-backup:
    image: my-app
    volumes:
      - /home/user/app_data:/app
    labels:
      # Main label which maestro is looking for
      docker-backup-maestro.backup.name: my-app
      # Host path or volume name with app data that will be mounted inside backup container
      docker-backup-maestro.backup.path: /home/user/app_data
      # Custom env vars that will be forwarded inside backup container
      docker-backup-maestro.backup.env.BACKUP_CRON_EXPRESSION: "0 2 * * *"
      docker-backup-maestro.backup.volume: /host/archive_app:/archive
```

Backup container template - backup.yml:

```yml
image: offen/docker-volume-backup:latest
restart: always
volumes:
  - /var/run/docker.sock:/var/run/docker.sock:ro
environment:
  BACKUP_COMPRESSION: gz
```

## How to restore and force-backup container

Restore and force-backup containers are started using separate templates. By default these templates overlay basic backup template. Typically the only line in restore template is command, that overrides basic backup containers default behavior to run crond for example.

After restore template is provided you can run restore command to restore data.

`docker exec docker-backup-maestro maestro restore <name>`

This command will temporary stop backup container, then run restore container, wait for it to exit and start back backup container.

The same way works force-backup container, but it is intended for instant backup, if you could not wait for next backup schedule.

## Configuration

### Environment variables for docker-backup-maestro

`BIND_PATH` - path inside backup container where app data will be mounted. Default: `/data`

`BACKUP_TMPL_PATH` - path inside maestro container, where backup template is located. Default: `/root/backup_tmpl.yml`

`RESTORE_TMPL_PATH` - path inside maestro container, where restore template is located. Default: `/root/restore_tmpl.yml`

`FORCEBACKUP_TMPL_PATH`- path inside maestro container, where force backup template is located. Default:`/root/forcebackup_tmpl.yml`

`LABEL_PREFIX` - custom prefix for all labels. May be overrided to run multiple independent docker-backup-maestro configurations. Default: `docker-backup-maestro`

`BACKUP_NAME_FORMAT` - format string for backup container name. Replaces '{name}' substring with backup name (taken from label). Default: `${LABEL_PREFIX}.backup_{name}`

`RESTORE_NAME_FORMAT`- format string for restore container name. Replaces '{name}' substring with backup name (taken from label). Default:`${LABEL_PREFIX}.restore_{name}`

`FORCEBACKUP_NAME_FORMAT`- format string for force backup container name. Replaces '{name}' substring with backup name (taken from label). Default:`${LABEL_PREFIX}.forcebackup_{name}`

`RESTORE_NO_OVERLAY` - do not overlay restore template over backup template, instead restore template is self-sufficient template. Default: `FALSE`

`FORCEBACKUP_NO_OVERLAY`- do not overlay force backup template over basic backup template, instead force backup template is self-sufficient template. Default:`FALSE`

`BACKUP_TAG` - tag for backup image, if it need to be built. Default: `${LABEL_PREFIX}.backup`

`RESTORE_TAG` - tag for restore image, if it need to be built. Default: `${LABEL_PREFIX}.restore`

`FORCEBACKUP_TAG` - tag for force backup image, if it need to be built. Default: `${LABEL_PREFIX}.forcebackup`

`ALWAYS_RW` - if `TRUE`, then data mounts in backup containers will be mounted without ro flag always. Default: `FALSE`

`BUILDER_V1` - if `TRUE`, then old docker builder v1 used to build images instead of BuildKit. Sometimes helps to overcome issues and bugs during build. Default: `FALSE`

### Labels for app containers

Labels on app containers are used to setup apps companion container. Setting this labels allows to have different settings on each companion container. Here are label names provided based on default label prefix `docker-backup-maestro` changed with env `LABEL_PREFIX`

Anywhere backup container is mentioned here, the same applies for restore and force backup containers as well.

`docker-backup-maestro.backup.name` - required label that identifies app containers that maestro will work with. Must be unique name for each target container.

`docker-backup-maestro.backup.path` - path on host OS or volume name that will be mounted in backup container to BIND_PATH path. Not required. Multiple paths could be provided using suffixes. Each path then will be mounted in suffix dir inside BIND_PATH. In case of using suffixes, basic label with no suffix is ignored. Example: `docker-backup-maestro.backup.path.app=/host/app` `docker-backup-maestro.backup.path.db=/host/db` will be mount in /data/app and /data/db if BIND_PATH is /data

Note that by default docker-backup-maestro will mount this path to backup and force backup containers with ro flag, and to restore container without ro flag (rw). This could be changed with environment setting (ALWAYS_RW).

`docker-backup-maestro.backup.networks` - comma separated list of docker network names backup container will be connected to.

`docker-backup-maestro.backup.env.<ENV>` - this label forwards `<ENV>` environment var into companion backup container. Value of this label is passed as ENV value. It is possible to forward any number of environment vars. Example: label `docker-backup-maestro.backup.env.VAR=val` results in env `VAR=val` inside backup container.

`docker-backup-maestro.backup.volume` - this label may contain volume bind string using format "<host_path>:<container_path>[:ro]". The volume will be added to backup container. Host path must be absolute. To use multiple volumes you can use multiple labels adding some different suffix, example:

`docker-backup-maestro.backup.volume.cache=/tmp/cache:/cache` Suffix itself does not mean anything.

### Template for companion backup containers

Templates are compose service-like configs with restricted amount of fields. Environment variables could be used anywhere in template, they will be resolved and substituted when template is read.

Supported fields:

```yml
/# Image used to create companion containers or to tag built images
image: <image>
# Build instructs to build image. Note that you need to mount build context inside docker-backup-maestro container and use its path in context here.
# If dockerfile path is default (Dockerfile in context root) then simple form could be used: 
# build: /build
build:
  context: /build
  dockerfile: dir/dockerfile
# Container entrypoint in list format
entrypoint: [ "override", "entrypoint" ]
# Container command in list format
command: [ "custom", "command" ]
# Restart policy. Supported values identical to compose
restart: unless-stopped
# Env file used for companion containers. Must be located inside maestro container (mounted from host). It is possible to use array for multiple files
env_file: /root/backup.env
# List of volumes and binds mounted in each companion container. Host path must be absolute
volumes:
  - <host_path or volume name>:<backup_container_path>
# Env vars present in each companion container. Both array and dict forms supported
environment:
  ENV_NAME: env_value
# Labels used in each companion container. Both array and dict forms supported
labels:
  label-name: label-value
# List of networks used in each companion container
networks:
  - custom_network
# List of devices that will be available inside companion containers
devices:
  - /dev/zfs:/dev/zfs
# If companion container will be privileged
privileged: true
```

If you need other compose fields, feel free to post an issue with feature request.

## CLI commands

```
Utility to auto start/stop backup containers

Usage:
  maestro [flags]
  maestro [command]

Available Commands:
  build-all         Build backup restore and force-backup containers
  build-backup      Build backup container
  build-force       Build force-backup container
  build-restore     Build restore container
  create            Create backup container
  create-all        Create all backup containers
  force-backup      Force backup container
  force-backup-all  Force backup all available containers (optionally include stopped)
  help              Help about any command
  list              List containers labeled for backup
  pull-all          Pull images for backup, restore and force-backup containers
  pull-backup       Pull image for backup container
  pull-force-backup Pull image for force-backup container
  pull-restore      Pull image for restore container
  remove            Remove backup container
  remove-all        Remove all backup containers
  restore           Restore container
  restore-all       Restore all available containers (including stopped)
  start             Start previously stopped backup container
  start-all         Start all previously stopped backup containers
  stop              Stop backup/restore container
  stop-all          Stop all backup/restore containers

Flags:
  -h, --help   help for maestro

Use "maestro [command] --help" for more information about a command.

```
