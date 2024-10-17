package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/anpavlov/docker-backup-mastro.git/backuper"
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

	backuperTmpl, err := backuper.ReadTemplateFromFile(cfg.BackuperTemplatePath)
	if err != nil {
		log.Fatalln(err)
	}

	restoreTmpl, err := backuper.ReadTemplateFromFile(cfg.RestoreTemplatePath)
	if err != nil {
		log.Fatalln(err)
	}

	mngr := NewContainerManager(cli, UserTemplates{Backuper: backuperTmpl, Restore: restoreTmpl}, cfg)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cmd := NewRootCmd(mngr)
	err = cmd.ExecuteContext(ctx)
	if err != nil {
		log.Fatalln("error while running:", err)
	}

}
