package main

import (
	"context"
	"log"
	"log/slog"
	"os"

	"github.com/tez-capital/tezsign/common"
	"github.com/urfave/cli/v3"
)

type hostCtxKey struct{}

type HostContext struct {
	Log     *slog.Logger
	Session *common.Session
}

func main() {
	app := &cli.Command{
		Name:  "tezsign-host",
		Usage: "USB host CLI for TezSign gadget (signer)",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "device",
				Aliases: []string{"d"},
				Usage:   "USB serial to select (if multiple gadgets present)",
				Sources: cli.EnvVars(envDevice),
			},
		},
		After: closeSession,
		Commands: []*cli.Command{
			withBefore(cmdListDevices(), withLoggerOnly()),      // no session needed
			withBefore(cmdRun(), withSession(common.ChanSign)),  // signer interface
			withBefore(cmdInit(), withSession(common.ChanMgmt)), // mgmt interface
			withBefore(cmdList(), withSession(common.ChanMgmt)),
			withBefore(cmdNewKeys(), withSession(common.ChanMgmt)),
			withBefore(cmdStatus(), withSession(common.ChanMgmt)),
			withBefore(cmdUnlockKeys(), withSession(common.ChanMgmt)),
			withBefore(cmdLockKeys(), withSession(common.ChanMgmt)),
			withBefore(cmdDeleteKeys(), withSession(common.ChanMgmt)),

			cmdAdvanced(),
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}
