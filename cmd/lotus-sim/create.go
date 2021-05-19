package main

import (
	"fmt"

	"github.com/filecoin-project/lotus/chain/types"
	lcli "github.com/filecoin-project/lotus/cli"
	"github.com/urfave/cli/v2"
)

var createSimCommand = &cli.Command{
	Name:      "create",
	ArgsUsage: "[tipset]",
	Action: func(cctx *cli.Context) error {
		node, err := open(cctx)
		if err != nil {
			return err
		}
		defer node.Close()

		var ts *types.TipSet
		switch cctx.NArg() {
		case 0:
			if err := node.Chainstore.Load(); err != nil {
				return err
			}
			ts = node.Chainstore.GetHeaviestTipSet()
		case 1:
			cids, err := lcli.ParseTipSetString(cctx.Args().Get(1))
			if err != nil {
				return err
			}
			tsk := types.NewTipSetKey(cids...)
			ts, err = node.Chainstore.LoadTipSet(tsk)
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("expected 0 or 1 arguments")
		}
		_, err = node.CreateSim(cctx.Context, cctx.String("simulation"), ts)
		return err
	},
}
