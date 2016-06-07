package main

import (
	"fmt"
	"gcredstash/command"
	"github.com/mitchellh/cli"
	"os"
)

func Run(args []string) int {
	// Meta-option for executables.
	// It defines output color and its stdout/stderr stream.
	meta := &command.Meta{
		Ui: &cli.ColoredUi{
			InfoColor:  cli.UiColorBlue,
			ErrorColor: cli.UiColorRed,
			Ui: &cli.BasicUi{
				Writer:      os.Stdout,
				ErrorWriter: os.Stderr,
				Reader:      os.Stdin,
			},
		},
		Table:  os.Getenv("GCREDSTASH_TABLE"),
		KmsKey: os.Getenv("GCREDSTASH_KMS_KEY"),
	}

	if meta.Table == "" {
		meta.Table = "credential-store"
	}

	if meta.KmsKey == "" {
		meta.KmsKey = "alias/credstash"
	}

	return RunCustom(args, Commands(meta))
}

func RunCustom(args []string, commands map[string]cli.CommandFactory) int {
	cli := &cli.CLI{
		Args:       args,
		Commands:   commands,
		Version:    Version,
		HelpFunc:   cli.BasicHelpFunc(Name),
		HelpWriter: os.Stdout,
	}

	exitCode, err := cli.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to execute: %s\n", err.Error())
	}

	return exitCode
}
