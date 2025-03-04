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
		Use:           filepath.Base(os.Args[0]),
		Short:         "Utility to auto start/stop backup containers",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Println("Starting maestro")
			return mngr.Run(cmd.Context())
		},
	}

	rootCmd.CompletionOptions.HiddenDefaultCmd = true

	restoreCmd := &cobra.Command{
		Use:   "restore name",
		Short: "Restore container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Println("Restoring")

			return mngr.Restore(cmd.Context(), args[0])
		},
	}

	restoreAllCmd := &cobra.Command{
		Use:   "restore-all",
		Short: "Restore all available containers (including stopped)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return mngr.RestoreAll(cmd.Context())
		},
	}

	forceBackupCmd := &cobra.Command{
		Use:   "force-backup name",
		Short: "Force backup container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Println("Running force backup")

			return mngr.ForceBackup(cmd.Context(), args[0])
		},
	}

	var includeStopped bool

	forceBackupAllCmd := &cobra.Command{
		Use:   "force-backup-all",
		Short: "Force backup all available containers (optionally include stopped)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return mngr.ForceBackupAll(cmd.Context(), includeStopped)
		},
	}

	forceBackupAllCmd.Flags().BoolVar(&includeStopped, "include-stopped", false, "include stopped containers")

	buildAllCmd := &cobra.Command{
		Use:   "build-all",
		Short: "Build backup restore and force-backup containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			return mngr.BuildAll(cmd.Context())
		},
	}

	buildBackuperCmd := &cobra.Command{
		Use:   "build-backup",
		Short: "Build backup container",
		RunE: func(cmd *cobra.Command, args []string) error {
			return mngr.BuildBackuper(cmd.Context())
		},
	}

	buildRestoreCmd := &cobra.Command{
		Use:   "build-restore",
		Short: "Build restore container",
		RunE: func(cmd *cobra.Command, args []string) error {
			return mngr.BuildRestore(cmd.Context())
		},
	}

	buildForceCmd := &cobra.Command{
		Use:   "build-force",
		Short: "Build force-backup container",
		RunE: func(cmd *cobra.Command, args []string) error {
			return mngr.BuildForce(cmd.Context())
		},
	}

	stopCmd := &cobra.Command{
		Use:   "stop name",
		Short: "Stop backup/restore container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return mngr.Stop(cmd.Context(), args[0])
		},
	}

	stopAllCmd := &cobra.Command{
		Use:   "stop-all",
		Short: "Stop all backup/restore containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			return mngr.StopAll(cmd.Context())
		},
	}

	startCmd := &cobra.Command{
		Use:   "start name",
		Short: "Start previously stopped backup container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return mngr.StartBackuper(cmd.Context(), args[0])
		},
	}

	startAllCmd := &cobra.Command{
		Use:   "start-all",
		Short: "Start all previously stopped backup containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			return mngr.StartAll(cmd.Context())
		},
	}

	createCmd := &cobra.Command{
		Use:   "create name",
		Short: "Create backup container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return mngr.CreateBackuper(cmd.Context(), args[0])
		},
	}

	createAllCmd := &cobra.Command{
		Use:   "create-all",
		Short: "Create all backup containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			return mngr.CreateAll(cmd.Context())
		},
	}

	removeCmd := &cobra.Command{
		Use:   "remove name",
		Short: "Remove backup and restore container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return mngr.RemoveBackuper(cmd.Context(), args[0])
		},
	}

	removeAllCmd := &cobra.Command{
		Use:   "remove-all",
		Short: "Remove all backup and restore containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			return mngr.RemoveAll(cmd.Context())
		},
	}

	pullBackupCmd := &cobra.Command{
		Use:   "pull-backup",
		Short: "Pull image for backup container",
		RunE: func(cmd *cobra.Command, args []string) error {
			return mngr.PullBackuper(cmd.Context())
		},
	}

	pullRestoreCmd := &cobra.Command{
		Use:   "pull-restore",
		Short: "Pull image for restore container",
		RunE: func(cmd *cobra.Command, args []string) error {
			return mngr.PullRestore(cmd.Context())
		},
	}

	pullForceCmd := &cobra.Command{
		Use:   "pull-force-backup",
		Short: "Pull image for force-backup container",
		RunE: func(cmd *cobra.Command, args []string) error {
			return mngr.PullForce(cmd.Context())
		},
	}

	pullAllCmd := &cobra.Command{
		Use:   "pull-all",
		Short: "Pull images for backup, restore and force-backup containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			return mngr.PullAll(cmd.Context())
		},
	}

	var listOpts ListOptions

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List containers labeled for backup",
		RunE: func(cmd *cobra.Command, args []string) error {
			return mngr.List(cmd.Context(), listOpts)
		},
	}

	listCmd.Flags().BoolVar(&listOpts.All, "all", false, "include stopped containers")
	listCmd.Flags().BoolVar(&listOpts.Backupers, "backup", false, "list backup containers instead")
	listCmd.Flags().BoolVar(&listOpts.Restores, "restore", false, "list restore containers instead")
	listCmd.Flags().BoolVar(&listOpts.ForceBackups, "force-backup", false, "list force-backup containers instead")
	listCmd.MarkFlagsMutuallyExclusive("backup", "restore", "force-backup")

	rootCmd.AddCommand(
		restoreCmd,
		restoreAllCmd,
		forceBackupCmd,
		forceBackupAllCmd,
		buildAllCmd,
		buildBackuperCmd,
		buildRestoreCmd,
		buildForceCmd,
		stopCmd,
		stopAllCmd,
		startCmd,
		startAllCmd,
		pullBackupCmd,
		pullRestoreCmd,
		pullForceCmd,
		pullAllCmd,
		listCmd,
		createCmd,
		createAllCmd,
		removeCmd,
		removeAllCmd,
	)

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
		if restoreTmpl == nil {
			restoreTmpl = &Template{}
		}
		restoreTmpl = backuperTmpl.Overlay(restoreTmpl)
	}

	forceTmpl, err := ReadTemplateFromFile(cfg.ForceBackupTemplatePath, false)
	if err != nil {
		log.Fatalln(err)
	}

	if !cfg.NoForceBackupOverlay {
		if forceTmpl == nil {
			forceTmpl = &Template{}
		}
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
