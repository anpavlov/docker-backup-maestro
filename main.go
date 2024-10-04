package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/anpavlov/docker-backup-mastro.git/backuper"
	"github.com/docker/docker/client"
)

func main() {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalln("failed to create docker client:", err)
	}

	userCfg := &backuper.Config{
		Image:   "alpine",
		Command: "cp -pr /data $TARGET",
		Binds:   map[string]string{"bvol": "/backup"},
	}

	var cfg Config
	cfg.Backuper.BindToPath = "/data"

	mngr := NewContainerManager(cli, userCfg, cfg)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	log.Println("Starting")
	err = mngr.Run(ctx)
	if err != nil {
		log.Fatalln("error while running:", err)
	}
}
