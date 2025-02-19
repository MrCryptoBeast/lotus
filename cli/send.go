package cli

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/urfave/cli/v2"
	cbg "github.com/whyrusleeping/cbor-gen"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/abi"

	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/chain/actors/builtin"
	"github.com/filecoin-project/lotus/chain/stmgr"
	"github.com/filecoin-project/lotus/chain/types"
	"github.com/filecoin-project/lotus/chain/wallet"
)

var sendCmd = &cli.Command{
	Name:      "send",
	Usage:     "Send funds between accounts",
	ArgsUsage: "[targetAddress] [amount]",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "from",
			Usage: "optionally specify the account to send funds from",
		},
		&cli.StringFlag{
			Name:  "gas-premium",
			Usage: "specify gas price to use in AttoFIC",
			Value: "0",
		},
		&cli.StringFlag{
			Name:  "gas-feecap",
			Usage: "specify gas fee cap to use in AttoFIC",
			Value: "0",
		},
		&cli.Int64Flag{
			Name:  "gas-limit",
			Usage: "specify gas limit",
			Value: 0,
		},
		&cli.Uint64Flag{
			Name:  "nonce",
			Usage: "specify the nonce to use",
			Value: 0,
		},
		&cli.Uint64Flag{
			Name:  "method",
			Usage: "specify method to invoke",
			Value: uint64(builtin.MethodSend),
		},
		&cli.StringFlag{
			Name:  "params-json",
			Usage: "specify invocation parameters in json",
		},
		&cli.StringFlag{
			Name:  "params-hex",
			Usage: "specify invocation parameters in hex",
		},
		&cli.BoolFlag{
			Name:  "force",
			Usage: "must be specified for the action to take effect if maybe SysErrInsufficientFunds etc",
		},
	},
	Action: func(cctx *cli.Context) error {
		if cctx.Args().Len() != 2 {
			return ShowHelp(cctx, fmt.Errorf("'send' expects two arguments, target and amount"))
		}

		nodeApi, closer, err := GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()

		ctx := ReqContext(cctx)

		toAddr, err := address.NewFromString(cctx.Args().Get(0))
		if err != nil {
			return ShowHelp(cctx, fmt.Errorf("failed to parse target address: %w", err))
		}

		val, err := types.ParseFIL(cctx.Args().Get(1))
		if err != nil {
			return ShowHelp(cctx, fmt.Errorf("failed to parse amount: %w", err))
		}

		var fromAddr address.Address
		if from := cctx.String("from"); from == "" {
			defaddr, err := nodeApi.WalletDefaultAddress(ctx)
			if err != nil {
				return err
			}

			fromAddr = defaddr
		} else {
			addr, err := address.NewFromString(from)
			if err != nil {
				return err
			}

			fromAddr = addr
		}

		gp, err := types.BigFromString(cctx.String("gas-premium"))
		if err != nil {
			return err
		}
		gfc, err := types.BigFromString(cctx.String("gas-feecap"))
		if err != nil {
			return err
		}

		method := abi.MethodNum(cctx.Uint64("method"))

		var params []byte
		if cctx.IsSet("params-json") {
			decparams, err := decodeTypedParams(ctx, nodeApi, toAddr, method, cctx.String("params-json"))
			if err != nil {
				return fmt.Errorf("failed to decode json params: %w", err)
			}
			params = decparams
		}
		if cctx.IsSet("params-hex") {
			if params != nil {
				return fmt.Errorf("can only specify one of 'params-json' and 'params-hex'")
			}
			decparams, err := hex.DecodeString(cctx.String("params-hex"))
			if err != nil {
				return fmt.Errorf("failed to decode hex params: %w", err)
			}
			params = decparams
		}

		msg := &types.Message{
			From:       fromAddr,
			To:         toAddr,
			Value:      types.BigInt(val),
			GasPremium: gp,
			GasFeeCap:  gfc,
			GasLimit:   cctx.Int64("gas-limit"),
			Method:     method,
			Params:     params,
		}

		if wallet.GetSetupStateForLocal(getWalletRepo(cctx)) {
			rest, err := nodeApi.WalletCustomMethod(ctx, api.WalletIsLock, []interface{}{})
			if err != nil {
				return err
			}

			if state := rest.(bool); state {
				return fmt.Errorf("wallet is lock, dont send msg, please unlock wallet ! ")
			}

		}

		if !cctx.Bool("force") {
			// Funds insufficient check
			fromBalance, err := nodeApi.WalletBalance(ctx, msg.From)
			if err != nil {
				return err
			}
			totalCost := types.BigAdd(types.BigMul(msg.GasFeeCap, types.NewInt(uint64(msg.GasLimit))), msg.Value)

			if fromBalance.LessThan(totalCost) {
				fmt.Printf("WARNING: From balance %s less than total cost %s\n", types.FIL(fromBalance), types.FIL(totalCost))
				return fmt.Errorf("--force must be specified for this action to have an effect; you have been warned")
			}
		}

		if cctx.IsSet("nonce") {
			msg.Nonce = cctx.Uint64("nonce")
			sm, err := nodeApi.WalletSignMessage(ctx, fromAddr, msg)
			if err != nil {
				return err
			}

			_, err = nodeApi.MpoolPush(ctx, sm)
			if err != nil {
				return err
			}
			fmt.Println(sm.Cid())

		} else {
			sm, err := nodeApi.MpoolPushMessage(ctx, msg, nil)
			if err != nil {
				return err
			}
			fmt.Println(sm.Cid())
		}

		return nil
	},
}

func decodeTypedParams(ctx context.Context, fapi api.FullNode, to address.Address, method abi.MethodNum, paramstr string) ([]byte, error) {
	act, err := fapi.StateGetActor(ctx, to, types.EmptyTSK)
	if err != nil {
		return nil, err
	}

	methodMeta, found := stmgr.MethodsMap[act.Code][method]
	if !found {
		return nil, fmt.Errorf("method %d not found on actor %s", method, act.Code)
	}

	p := reflect.New(methodMeta.Params.Elem()).Interface().(cbg.CBORMarshaler)

	if err := json.Unmarshal([]byte(paramstr), p); err != nil {
		return nil, fmt.Errorf("unmarshaling input into params type: %w", err)
	}

	buf := new(bytes.Buffer)
	if err := p.MarshalCBOR(buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
