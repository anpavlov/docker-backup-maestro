package main

type Config struct {
	Backuper struct {
		BindToPath string `env:"BIND_PATH" envDefault:"/data"`
	}

	LabelPrefix          string `env:"LABEL_PREFIX"`
	BackuperTemplatePath string `env:"BACKUP_TMPL_PATH" envDefault:"/root/backuper_tmpl.yml"`
	RestoreTemplatePath  string `env:"RESTORE_TMPL_PATH" envDefault:"/root/restore_tmpl.yml"`
}
