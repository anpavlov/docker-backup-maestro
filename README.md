# docker-backup-maestro

Automatically start stop companion containers to backup data from another containers. May be used to auto start any companion containers, not only for backup.

## How does it work

docker-backup-maestro (maestro) is watching containers with specific labels. When any of them starts or stops, maestro will create and run or stop and delete companion container, using container template (compose-like) and labels from target container.

docker-backup-maestro provides **restore** and **forcebackup** cli commands (used with docker exec) that runs separate one-off containers with autoremove flag from their own templates.

## Usage with docker compose

docker-backup-maestro container

```yml
services:

  maestro:
    image: docker-backup-maestro:latest
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro

      # Compose-like templates for backup and restore containers. Reference below
      - /home/user/maestro/backup.yml:/root/backup.yml
      - /home/user/maestro/restore.yml:/root/restore.yml
    environment:
    #   BIND_PATH: /backup_data
    #   BACKUP_TMPL_PATH: /other/backup.yml
    #   RESTORE_TMPL_PATH: /ot/backuper.yml
    #   BACKUP_PASSPHRASE: mypassword
```

Setup target container to backup with labels

```yml
services:

  app-to-backup:
    image: my-app
    volumes:
      - /home/user/app_data:/data
    labels:
      # Main label which maestro is looking for
      docker-backup-maestro.backup.name: my-app
      # Host path or volume name with app data that will be mounted inside backup container
      docker-backup-maestro.backup.path: /home/user/app_data
      # Custom env vars that will be forwarded inside backup container
      docker-backup-maestro.backup.env.BACKUP_DEST: /backup_dest
      docker-backup-maestro.backup.env.CRON_SCHEDULE: 0 3 * * *
```

Backup container template - backup.yml:

```yml
image: docker-restic-cron
# volumes:
#   - ${MEDIA_CONFIGS}:/data:ro
  # - /var/run/docker.sock:/var/run/docker.sock
volumes:
  - backup_dst_vol:/backup_dest
environment:
  PATH_TO_BACKUP: ${BIND_PATH}
  CLEANUP_COMMAND: --prune --keep-last 10
  RCLONE_VERBOSE: 1
  RCLONE_RETRIES: 20
  RCLONE_LOW_LEVEL_RETRIES: 20
# SKIP_ON_START=true
#Wset host externally to override docker-host (individually for each container) default value for backup container
#tavoid expensive rescan filesystem (used parent snapshot when backing up: used last snapshot with same host)
  OPT_ARGUMENTS: --host myhost
  RESTIC_PASSWORD: ${BACKUP_PASSPHRASE}
#Uomment to force backup immediately after start
  SKIP_ON_START: false
#Uommment to restore latest backup
#-ESTORE_ON_EMPTY_START=true
# BACKUP_DEST=${MEDIA_BACKUP_FOLDER}
# DEPENDENT_CONTAINERS=jellyfin_app
```

## Configuration

### Environment variables for docker-backup-maestro

`BIND_PATH` - path inside backup container where app data will be mounted. Default: `/data`

`BACKUP_TMPL_PATH` - path inside maestro container, where backup template is located. Default: `/root/backup_tmpl.yml`

`RESTORE_TMPL_PATH` - path inside maestro container, where restore template is located. Default: `/root/restore_tmpl.yml`

`LABEL_PREFIX` - custom prefix for all labels. May be overrided to run multiple independent docker-backup-maestro configurations. Default: `docker-backup-maestro`

## Labels for app containers

Labels on app containers are used to setup apps companion container. Setting this labels allows to have different settings on each companion container.

`docker-backup-maestro.backup.name` - required label that identifies app containers that maestro will work with. Must be unique name for each target container.

`docker-backup-maestro.backup.path` - path inside app container or volume name that will be mounted in backup container

`docker-backup-maestro.backup.network` - name of docker network. Backup container will be connected to it.

`docker-backup-maestro.backup.env.<ENV>` - this label forwards <ENV> environment var into companion backup container. Value of this label is passed as ENV value. It is possible to forward any number of environment vars.

## Template for companion backup containers

Templates are compose service-like configs with restricted amount of fields.

Supported fields: 

```yml
# Image used to create companion containers
image: <image>
# Container entrypoint in list format
entrypoint: [ "override", "entrypoint" ]
# Container command in list format
entrypoint: [ "custom", "command" ]
# Restart policy. Supported values identical to compose
restart: unless-stopped
# Env file used for companion containers. Must be located inside maestro container (mounted from host)!
env_file: /root/backup.env
# List of volumes and binds mounted in each companion container
volumes:
  - <host_path or volume name>:<backup_container_path>
# Env vars present in each companion container
environment:
  ENV_NAME: env_value
# Labels used in each companion container
labels:
  label-name: label-value
# List of networks used in each companion container
networks:
  - custom_network
```

If you need other compose fields, feel free to post an issue with feature request.

## Restore and force backup

readme todo:
- restore and force backup commands
- backup mounts data ro, restore - rw
- restore from scratch using container create
