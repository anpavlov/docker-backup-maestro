package internal

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/caarlos0/env/v11"
	"github.com/docker/docker/client"
	"github.com/spf13/cobra"
)

func NewRootCmd(mngr *ContainerManager) *cobra.Command {
	rootCmd := &cobra.Command{
		Use:          filepath.Base(os.Args[0]),
		Short:        "Utility to auto start/stop backup containers",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Println("Starting maestro")
			return mngr.Run(cmd.Context())
		},
	}

	restoreCmd := &cobra.Command{
		Use:   "restore name",
		Short: "Run restore container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Println("Restoring")

			if len(args) != 1 {
				log.Fatalln("restore name not passed")
			}

			return mngr.StartRestore(cmd.Context(), args[0])
		},
	}

	forceBackupCmd := &cobra.Command{
		Use:   "forcebackup name",
		Short: "Run force backup container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Println("Running force backup")

			if len(args) != 1 {
				log.Fatalln("forcebackup name not passed")
			}

			return mngr.StartForceBackup(cmd.Context(), args[0])
		},
	}

	rootCmd.AddCommand(restoreCmd, forceBackupCmd)

	return rootCmd
}

func RunApp() {
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
