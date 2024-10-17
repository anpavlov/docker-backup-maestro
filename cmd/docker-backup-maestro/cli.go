package main

import (
	"log"
	"os"
	"path/filepath"

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
