package main

type Config struct {
	Backuper struct {
		BindToPath string `env:"BIND_PATH" envDefault:"/data"`
	}

	LabelPrefix             string `env:"LABEL_PREFIX"`
	BackuperTemplatePath    string `env:"BACKUP_TMPL_PATH" envDefault:"/root/backup_tmpl.yml"`
	RestoreTemplatePath     string `env:"RESTORE_TMPL_PATH" envDefault:"/root/restore_tmpl.yml"`
	ForceBackupTemplatePath string `env:"FORCEBACKUP_TMPL_PATH" envDefault:"/root/forcebackup_tmpl.yml"`
}
