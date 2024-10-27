package internal

type Config struct {
	Backuper struct {
		BindToPath string `env:"BIND_PATH" envDefault:"/data"`
	}

	LabelPrefix string `env:"LABEL_PREFIX" envDefault:"docker-backup-maestro"`

	BackuperTemplatePath    string `env:"BACKUP_TMPL_PATH" envDefault:"/root/backup_tmpl.yml"`
	RestoreTemplatePath     string `env:"RESTORE_TMPL_PATH" envDefault:"/root/restore_tmpl.yml"`
	ForceBackupTemplatePath string `env:"FORCEBACKUP_TMPL_PATH" envDefault:"/root/forcebackup_tmpl.yml"`

	NoRestoreOverlay     bool `env:"RESTORE_NO_OVERLAY"`
	NoForceBackupOverlay bool `env:"FORCEBACKUP_NO_OVERLAY"`
}
