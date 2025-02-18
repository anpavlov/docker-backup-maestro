package internal

type Config struct {
	Backuper struct {
		BindToPath string `env:"BIND_PATH" envDefault:"/data"`
	}

	LabelPrefix string `env:"LABEL_PREFIX" envDefault:"docker-backup-maestro"`

	BackupNameFormat  string `env:"BACKUP_NAME_FORMAT,expand" envDefault:"${LABEL_PREFIX}.backup_{name}"`
	RestoreNameFormat string `env:"RESTORE_NAME_FORMAT,expand" envDefault:"${LABEL_PREFIX}.restore_{name}"`
	ForceNameFormat   string `env:"FORCEBACKUP_NAME_FORMAT,expand" envDefault:"${LABEL_PREFIX}.forcebackup_{name}"`

	BackuperTemplatePath    string `env:"BACKUP_TMPL_PATH" envDefault:"/root/backup_tmpl.yml"`
	RestoreTemplatePath     string `env:"RESTORE_TMPL_PATH" envDefault:"/root/restore_tmpl.yml"`
	ForceBackupTemplatePath string `env:"FORCEBACKUP_TMPL_PATH" envDefault:"/root/forcebackup_tmpl.yml"`

	NoRestoreOverlay     bool `env:"RESTORE_NO_OVERLAY"`
	NoForceBackupOverlay bool `env:"FORCEBACKUP_NO_OVERLAY"`

	BackupTag  string `env:"BACKUP_TAG,expand" envDefault:"${LABEL_PREFIX}.backup"`
	RestoreTag string `env:"RESTORE_TAG,expand" envDefault:"${LABEL_PREFIX}.restore"`
	ForceTag   string `env:"FORCEBACKUP_TAG,expand" envDefault:"${LABEL_PREFIX}.forcebackup"`

	AlwaysRw bool `env:"ALWAYS_RW"`

	BuilderV1 bool `env:"BUILDER_V1"`
}
