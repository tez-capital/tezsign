package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"strings"

	"github.com/tez-capital/tezsign/common"
	"github.com/urfave/cli/v3"
)

type hostCtxKey struct{}

type HostContext struct {
	Log     *slog.Logger
	Session *common.Session
}

func main() {
	args := rewriteVersionAlias(os.Args)

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
			withBefore(cmdListDevices(), withLoggerOnly()), // no session needed
			withBefore(cmdVersion(), withSession(common.ChanMgmt)),
			withBefore(cmdRun(), withSession(common.ChanSign)),  // signer interface
			withBefore(cmdInit(), withSession(common.ChanMgmt)), // mgmt interface
			withBefore(cmdList(), withSession(common.ChanMgmt)),
			withBefore(cmdNewKeys(), withSession(common.ChanMgmt)),
			withBefore(cmdStatus(), withSession(common.ChanMgmt)),
			withBefore(cmdLogs(), withSession(common.ChanMgmt)),
			withBefore(cmdUnlockKeys(), withSession(common.ChanMgmt)),
			withBefore(cmdLockKeys(), withSession(common.ChanMgmt)),
			withBefore(cmdDeleteKeys(), withSession(common.ChanMgmt)),

			cmdAdvanced(),
		},
	}

	if err := app.Run(context.Background(), args); err != nil {
		log.Fatal(err)
	}
}

func rewriteVersionAlias(args []string) []string {
	if len(args) <= 1 {
		return args
	}

	out := make([]string, len(args))
	copy(out, args)

	for i := 1; i < len(out); i++ {
		tok := out[i]

		if tok == "--version" {
			out[i] = "version"
			return out
		}

		switch {
		case tok == "--device" || tok == "-d":
			if i+1 < len(out) {
				i++
			}
		case strings.HasPrefix(tok, "--device="), strings.HasPrefix(tok, "-d="):
		case strings.HasPrefix(tok, "-"):
		default:
			return args
		}
	}

	return args
}
