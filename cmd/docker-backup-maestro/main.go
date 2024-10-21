package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/caarlos0/env/v11"
	"github.com/docker/docker/client"
)

func main() {
	var cfg Config
	err := env.Parse(&cfg)
	if err != nil {
		log.Fatalln("failed to set config:", err)
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalln("failed to create docker client:", err)
	}

	backuperTmpl, err := ReadTemplateFromFile(cfg.BackuperTemplatePath, true)
	if err != nil {
		log.Fatalln(err)
	}

	restoreTmpl, err := ReadTemplateFromFile(cfg.RestoreTemplatePath, false)
	if err != nil {
		log.Fatalln(err)
	}

	if !cfg.NoRestoreOverlay {
		restoreTmpl = backuperTmpl.Overlay(restoreTmpl)
	}

	forceTmpl, err := ReadTemplateFromFile(cfg.ForceBackupTemplatePath, false)
	if err != nil {
		log.Fatalln(err)
	}

	if !cfg.NoForceBackupOverlay {
		forceTmpl = backuperTmpl.Overlay(forceTmpl)
	}

	tmpls := UserTemplates{
		Backuper:    backuperTmpl,
		Restore:     restoreTmpl,
		ForceBackup: forceTmpl,
	}

	mngr := NewContainerManager(cli, tmpls, cfg)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cmd := NewRootCmd(mngr)
	err = cmd.ExecuteContext(ctx)
	if err != nil {
		log.Fatalln("error while running:", err)
	}

}
