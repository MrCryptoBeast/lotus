package cli

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	"github.com/urfave/cli/v2"
	"golang.org/x/xerrors"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/big"
	"github.com/filecoin-project/go-state-types/crypto"
	"github.com/mitchellh/go-homedir"

	lapi "github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/chain/types"
	"github.com/filecoin-project/lotus/chain/wallet"
	"github.com/filecoin-project/lotus/lib/tablewriter"
)

var walletCmd = &cli.Command{
	Name:  "wallet",
	Usage: "Manage wallet",
	Subcommands: []*cli.Command{
		walletNew,
		walletList,
		walletBalance,
		walletExport,
		walletImport,
		walletGetDefault,
		walletSetDefault,
		walletSign,
		walletVerify,
		walletDelete,
		walletMarket,
		walletLock,
		walletUnlock,
		walletIsLock,
		walletAddPasswd,
		walletChangePasswd,
		walletClearPasswd,
	},
}

var walletNew = &cli.Command{
	Name:      "new",
	Usage:     "Generate a new key of the given type",
	ArgsUsage: "[bls|secp256k1 (default secp256k1)]",
	Action: func(cctx *cli.Context) error {
		api, closer, err := GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()
		ctx := ReqContext(cctx)

		t := cctx.Args().First()
		if t == "" {
			t = "secp256k1"
		}

		nk, err := api.WalletNew(ctx, types.KeyType(t))
		if err != nil {
			return err
		}

		fmt.Println(nk.String())

		return nil
	},
}

var walletList = &cli.Command{
	Name:  "list",
	Usage: "List wallet address",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:    "addr-only",
			Usage:   "Only print addresses",
			Aliases: []string{"a"},
		},
		&cli.BoolFlag{
			Name:    "id",
			Usage:   "Output ID addresses",
			Aliases: []string{"i"},
		},
		&cli.BoolFlag{
			Name:    "market",
			Usage:   "Output market balances",
			Aliases: []string{"m"},
		},
	},
	Action: func(cctx *cli.Context) error {
		api, closer, err := GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()
		ctx := ReqContext(cctx)
		rest, err := api.WalletCustomMethod(ctx, lapi.WalletListEnc, []interface{}{})
		if err != nil {
			return err
		}
		addrs, _ := json.Marshal(rest)
		encryptAddrs := make([]lapi.AddrListEncrypt, 0)
		json.Unmarshal(addrs, &encryptAddrs)

		// Assume an error means no default key is set
		def, _ := api.WalletDefaultAddress(ctx)

		tw := tablewriter.New(
			tablewriter.Col("Address"),
			tablewriter.Col("ID"),
			tablewriter.Col("Balance"),
			tablewriter.Col("Market(Avail)"),
			tablewriter.Col("Market(Locked)"),
			tablewriter.Col("Nonce"),
			tablewriter.Col("Default"),
			tablewriter.NewLineCol("Error"))

		for _, encryptAddr := range encryptAddrs {
			if cctx.Bool("addr-only") {
				fmt.Println(encryptAddr.Addr.String())
			} else {
				a, err := api.StateGetActor(ctx, encryptAddr.Addr, types.EmptyTSK)
				if err != nil {
					if !strings.Contains(err.Error(), "actor not found") {
						tw.Write(map[string]interface{}{
							"Address": encryptAddr.Addr,
							"Error":   err,
						})
						continue
					}

					a = &types.Actor{
						Balance: big.Zero(),
					}
				}

				row := map[string]interface{}{
					"Address": encryptAddr.Addr,
					"Balance": types.FIL(a.Balance),
					"Nonce":   a.Nonce,
					"Encrypt": encryptAddr.Encrypt,
				}
				if encryptAddr.Addr == def {
					row["Default"] = "X"
				}

				if cctx.Bool("id") {
					id, err := api.StateLookupID(ctx, encryptAddr.Addr, types.EmptyTSK)
					if err != nil {
						row["ID"] = "n/a"
					} else {
						row["ID"] = id
					}
				}

				if cctx.Bool("market") {
					mbal, err := api.StateMarketBalance(ctx, encryptAddr.Addr, types.EmptyTSK)
					if err == nil {
						row["Market(Avail)"] = types.FIL(types.BigSub(mbal.Escrow, mbal.Locked))
						row["Market(Locked)"] = types.FIL(mbal.Locked)
					}
				}

				tw.Write(row)
			}
		}

		if !cctx.Bool("addr-only") {
			return tw.Flush(os.Stdout)
		}

		return nil
	},
}

var walletBalance = &cli.Command{
	Name:      "balance",
	Usage:     "Get account balance",
	ArgsUsage: "[address]",
	Action: func(cctx *cli.Context) error {
		api, closer, err := GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()
		ctx := ReqContext(cctx)

		var addr address.Address
		if cctx.Args().First() != "" {
			addr, err = address.NewFromString(cctx.Args().First())
		} else {
			addr, err = api.WalletDefaultAddress(ctx)
		}
		if err != nil {
			return err
		}

		balance, err := api.WalletBalance(ctx, addr)
		if err != nil {
			return err
		}

		if balance.Equals(types.NewInt(0)) {
			fmt.Printf("%s (warning: may display 0 if chain sync in progress)\n", types.FIL(balance))
		} else {
			fmt.Printf("%s\n", types.FIL(balance))
		}

		return nil
	},
}

var walletGetDefault = &cli.Command{
	Name:  "default",
	Usage: "Get default wallet address",
	Action: func(cctx *cli.Context) error {
		api, closer, err := GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()
		ctx := ReqContext(cctx)

		addr, err := api.WalletDefaultAddress(ctx)
		if err != nil {
			return err
		}

		fmt.Printf("%s\n", addr.String())
		return nil
	},
}

var walletSetDefault = &cli.Command{
	Name:      "set-default",
	Usage:     "Set default wallet address",
	ArgsUsage: "[address]",
	Action: func(cctx *cli.Context) error {
		api, closer, err := GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()
		ctx := ReqContext(cctx)

		if !cctx.Args().Present() {
			return fmt.Errorf("must pass address to set as default")
		}

		addr, err := address.NewFromString(cctx.Args().First())
		if err != nil {
			return err
		}

		return api.WalletSetDefault(ctx, addr)
	},
}

var walletExport = &cli.Command{
	Name:      "export",
	Usage:     "export keys",
	ArgsUsage: "[address]",
	Action: func(cctx *cli.Context) error {
		api, closer, err := GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()
		ctx := ReqContext(cctx)

		if !cctx.Args().Present() {
			return fmt.Errorf("must specify key to export")
		}

		defer func() {
			if err := recover(); err != nil {
				fmt.Println("invaild address payload")
			}
		}()

		addr, err := address.NewFromString(cctx.Args().First())
		if err != nil {
			return err
		}

		passwd := ""
		var ki *types.KeyInfo
		if wallet.GetSetupStateForLocal(getWalletRepo(cctx)) {
			// passwd := cctx.String("passwd")
			passwd = wallet.Prompt("Enter your Password:\n")
			if passwd == "" {
				return xerrors.Errorf("must enter your passwd")
			}
			rest, err := api.WalletCustomMethod(ctx, lapi.WalletExportForEnc, []interface{}{addr, passwd})
			if err != nil {
				return err
			}
			ki = rest.(*types.KeyInfo)
		} else {
			ki, err = api.WalletExport(ctx, addr)
			if err != nil {
				return err
			}
		}

		b, err := json.Marshal(ki)
		if err != nil {
			return err
		}

		fmt.Println(hex.EncodeToString(b))
		return nil
	},
}

var walletImport = &cli.Command{
	Name:      "import",
	Usage:     "import keys",
	ArgsUsage: "[<path> (optional, will read from stdin if omitted)]",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "format",
			Usage: "specify input format for key",
			Value: "hex-lotus",
		},
		&cli.BoolFlag{
			Name:  "as-default",
			Usage: "import the given key as your new default key",
		},
	},
	Action: func(cctx *cli.Context) error {
		api, closer, err := GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()
		ctx := ReqContext(cctx)

		var inpdata []byte
		if !cctx.Args().Present() || cctx.Args().First() == "-" {
			reader := bufio.NewReader(os.Stdin)
			fmt.Print("Enter private key: ")
			indata, err := reader.ReadBytes('\n')
			if err != nil {
				return err
			}
			inpdata = indata

		} else {
			fdata, err := ioutil.ReadFile(cctx.Args().First())
			if err != nil {
				return err
			}
			inpdata = fdata
		}

		var ki types.KeyInfo
		switch cctx.String("format") {
		case "hex-lotus":
			data, err := hex.DecodeString(strings.TrimSpace(string(inpdata)))
			if err != nil {
				return err
			}

			if err := json.Unmarshal(data, &ki); err != nil {
				return err
			}
		case "json-lotus":
			if err := json.Unmarshal(inpdata, &ki); err != nil {
				return err
			}
		case "gfc-json":
			var f struct {
				KeyInfo []struct {
					PrivateKey []byte
					SigType    int
				}
			}
			if err := json.Unmarshal(inpdata, &f); err != nil {
				return xerrors.Errorf("failed to parse go-filecoin key: %s", err)
			}

			gk := f.KeyInfo[0]
			ki.PrivateKey = gk.PrivateKey
			switch gk.SigType {
			case 1:
				ki.Type = types.KTSecp256k1
			case 2:
				ki.Type = types.KTBLS
			default:
				return fmt.Errorf("unrecognized key type: %d", gk.SigType)
			}
		default:
			return fmt.Errorf("unrecognized format: %s", cctx.String("format"))
		}

		addr, err := api.WalletImport(ctx, &ki)
		if err != nil {
			return err
		}

		if cctx.Bool("as-default") {
			if err := api.WalletSetDefault(ctx, addr); err != nil {
				return fmt.Errorf("failed to set default key: %w", err)
			}
		}

		fmt.Printf("imported key %s successfully!\n", addr)
		return nil
	},
}

var walletSign = &cli.Command{
	Name:      "sign",
	Usage:     "sign a message",
	ArgsUsage: "<signing address> <hexMessage>",
	Action: func(cctx *cli.Context) error {
		api, closer, err := GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()
		ctx := ReqContext(cctx)

		if !cctx.Args().Present() || cctx.NArg() != 2 {
			return fmt.Errorf("must specify signing address and message to sign")
		}

		addr, err := address.NewFromString(cctx.Args().First())

		if err != nil {
			return err
		}

		msg, err := hex.DecodeString(cctx.Args().Get(1))

		if err != nil {
			return err
		}

		sig, err := api.WalletSign(ctx, addr, msg)

		if err != nil {
			return err
		}

		sigBytes := append([]byte{byte(sig.Type)}, sig.Data...)

		fmt.Println(hex.EncodeToString(sigBytes))
		return nil
	},
}

var walletVerify = &cli.Command{
	Name:      "verify",
	Usage:     "verify the signature of a message",
	ArgsUsage: "<signing address> <hexMessage> <signature>",
	Action: func(cctx *cli.Context) error {
		api, closer, err := GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()
		ctx := ReqContext(cctx)

		if !cctx.Args().Present() || cctx.NArg() != 3 {
			return fmt.Errorf("must specify signing address, message, and signature to verify")
		}

		addr, err := address.NewFromString(cctx.Args().First())

		if err != nil {
			return err
		}

		msg, err := hex.DecodeString(cctx.Args().Get(1))

		if err != nil {
			return err
		}

		sigBytes, err := hex.DecodeString(cctx.Args().Get(2))

		if err != nil {
			return err
		}

		var sig crypto.Signature
		if err := sig.UnmarshalBinary(sigBytes); err != nil {
			return err
		}

		ok, err := api.WalletVerify(ctx, addr, msg, &sig)
		if err != nil {
			return err
		}
		if ok {
			fmt.Println("valid")
			return nil
		}
		fmt.Println("invalid")
		return NewCliError("CLI Verify called with invalid signature")
	},
}

var walletDelete = &cli.Command{
	Name:      "delete",
	Usage:     "Delete an account from the wallet",
	ArgsUsage: "<address> ",
	Action: func(cctx *cli.Context) error {
		api, closer, err := GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()
		ctx := ReqContext(cctx)

		if !cctx.Args().Present() || cctx.NArg() != 1 {
			return fmt.Errorf("must specify address to delete")
		}

		addr, err := address.NewFromString(cctx.Args().First())
		if err != nil {
			return err
		}

		passwd := ""
		if wallet.GetSetupStateForLocal(getWalletRepo(cctx)) {
			// passwd := cctx.String("passwd")
			passwd = wallet.Prompt("Enter your Password:\n")
			if passwd == "" {
				return xerrors.Errorf("Must enter your passwd")
			}
			_, err := api.WalletCustomMethod(ctx, lapi.WalletDeleteForEnc, []interface{}{addr, passwd})
			if err != nil {
				return err
			}
		} else {
			if err := api.WalletDelete(ctx, addr); err != nil {
				return err
			}
		}

		fmt.Println("delete an account success")
		return nil
	},
}

var walletMarket = &cli.Command{
	Name:  "market",
	Usage: "Interact with market balances",
	Subcommands: []*cli.Command{
		walletMarketWithdraw,
		walletMarketAdd,
	},
}

var walletMarketWithdraw = &cli.Command{
	Name:      "withdraw",
	Usage:     "Withdraw funds from the Storage Market Actor",
	ArgsUsage: "[amount (FIL) optional, otherwise will withdraw max available]",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "wallet",
			Usage:   "Specify address to withdraw funds to, otherwise it will use the default wallet address",
			Aliases: []string{"w"},
		},
		&cli.StringFlag{
			Name:    "address",
			Usage:   "Market address to withdraw from (account or miner actor address, defaults to --wallet address)",
			Aliases: []string{"a"},
		},
	},
	Action: func(cctx *cli.Context) error {
		api, closer, err := GetFullNodeAPI(cctx)
		if err != nil {
			return xerrors.Errorf("getting node API: %w", err)
		}
		defer closer()
		ctx := ReqContext(cctx)

		var wallet address.Address
		if cctx.String("wallet") != "" {
			wallet, err = address.NewFromString(cctx.String("wallet"))
			if err != nil {
				return xerrors.Errorf("parsing from address: %w", err)
			}
		} else {
			wallet, err = api.WalletDefaultAddress(ctx)
			if err != nil {
				return xerrors.Errorf("getting default wallet address: %w", err)
			}
		}

		addr := wallet
		if cctx.String("address") != "" {
			addr, err = address.NewFromString(cctx.String("address"))
			if err != nil {
				return xerrors.Errorf("parsing market address: %w", err)
			}
		}

		// Work out if there are enough unreserved, unlocked funds to withdraw
		bal, err := api.StateMarketBalance(ctx, addr, types.EmptyTSK)
		if err != nil {
			return xerrors.Errorf("getting market balance for address %s: %w", addr.String(), err)
		}

		reserved, err := api.MarketGetReserved(ctx, addr)
		if err != nil {
			return xerrors.Errorf("getting market reserved amount for address %s: %w", addr.String(), err)
		}

		avail := big.Subtract(big.Subtract(bal.Escrow, bal.Locked), reserved)

		notEnoughErr := func(msg string) error {
			return xerrors.Errorf("%s; "+
				"available (%s) = escrow (%s) - locked (%s) - reserved (%s)",
				msg, types.FIL(avail), types.FIL(bal.Escrow), types.FIL(bal.Locked), types.FIL(reserved))
		}

		if avail.IsZero() || avail.LessThan(big.Zero()) {
			avail = big.Zero()
			return notEnoughErr("no funds available to withdraw")
		}

		// Default to withdrawing all available funds
		amt := avail

		// If there was an amount argument, only withdraw that amount
		if cctx.Args().Present() {
			f, err := types.ParseFIL(cctx.Args().First())
			if err != nil {
				return xerrors.Errorf("parsing 'amount' argument: %w", err)
			}

			amt = abi.TokenAmount(f)
		}

		// Check the amount is positive
		if amt.IsZero() || amt.LessThan(big.Zero()) {
			return xerrors.Errorf("amount must be > 0")
		}

		// Check there are enough available funds
		if amt.GreaterThan(avail) {
			msg := fmt.Sprintf("can't withdraw more funds than available; requested: %s", types.FIL(amt))
			return notEnoughErr(msg)
		}

		fmt.Printf("Submitting WithdrawBalance message for amount %s for address %s\n", types.FIL(amt), wallet.String())
		smsg, err := api.MarketWithdraw(ctx, wallet, addr, amt)
		if err != nil {
			return xerrors.Errorf("fund manager withdraw error: %w", err)
		}

		fmt.Printf("WithdrawBalance message cid: %s\n", smsg)

		return nil
	},
}

var walletMarketAdd = &cli.Command{
	Name:      "add",
	Usage:     "Add funds to the Storage Market Actor",
	ArgsUsage: "<amount>",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "from",
			Usage:   "Specify address to move funds from, otherwise it will use the default wallet address",
			Aliases: []string{"f"},
		},
		&cli.StringFlag{
			Name:    "address",
			Usage:   "Market address to move funds to (account or miner actor address, defaults to --from address)",
			Aliases: []string{"a"},
		},
	},
	Action: func(cctx *cli.Context) error {
		api, closer, err := GetFullNodeAPI(cctx)
		if err != nil {
			return xerrors.Errorf("getting node API: %w", err)
		}
		defer closer()
		ctx := ReqContext(cctx)

		// Get amount param
		if !cctx.Args().Present() {
			return fmt.Errorf("must pass amount to add")
		}
		f, err := types.ParseFIL(cctx.Args().First())
		if err != nil {
			return xerrors.Errorf("parsing 'amount' argument: %w", err)
		}

		amt := abi.TokenAmount(f)

		// Get from param
		var from address.Address
		if cctx.String("from") != "" {
			from, err = address.NewFromString(cctx.String("from"))
			if err != nil {
				return xerrors.Errorf("parsing from address: %w", err)
			}
		} else {
			from, err = api.WalletDefaultAddress(ctx)
			if err != nil {
				return xerrors.Errorf("getting default wallet address: %w", err)
			}
		}

		// Get address param
		addr := from
		if cctx.String("address") != "" {
			addr, err = address.NewFromString(cctx.String("address"))
			if err != nil {
				return xerrors.Errorf("parsing market address: %w", err)
			}
		}

		// Add balance to market actor
		fmt.Printf("Submitting Add Balance message for amount %s for address %s\n", types.FIL(amt), addr)
		smsg, err := api.MarketAddBalance(ctx, from, addr, amt)
		if err != nil {
			return xerrors.Errorf("add balance error: %w", err)
		}

		fmt.Printf("AddBalance message cid: %s\n", smsg)

		return nil
	},
}

var walletLock = &cli.Command{
	Name:  "lock",
	Usage: "Lock wallet",
	Action: func(cctx *cli.Context) error {
		api, closer, err := GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()
		ctx := ReqContext(cctx)
		_, err = api.WalletCustomMethod(ctx, lapi.WalletLock, []interface{}{})
		if err != nil {
			return err
		}
		fmt.Println("lock wallet success")
		return nil
	},
}

var walletUnlock = &cli.Command{
	Name:  "unlock",
	Usage: "Unlock wallet",
	Action: func(cctx *cli.Context) error {
		api, closer, err := GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()
		ctx := ReqContext(cctx)
		if !wallet.GetSetupStateForLocal(getWalletRepo(cctx)) {
			fmt.Println("Passwd is not setup")
			return nil
		}
		passwd := wallet.Prompt("Enter your Password:\n")
		// passwd := cctx.String("passwd")
		if passwd == "" {
			return xerrors.Errorf("must enter your passwd")
		}

		_, err = api.WalletCustomMethod(ctx, lapi.WalletUnlock, []interface{}{passwd})
		if err != nil {
			return err
		}

		fmt.Println("unlock wallet success")
		return nil
	},
}

var walletIsLock = &cli.Command{
	Name:  "islock",
	Usage: "Check wallet lock state",
	Action: func(cctx *cli.Context) error {
		api, closer, err := GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()
		ctx := ReqContext(cctx)
		rest, err := api.WalletCustomMethod(ctx, lapi.WalletIsLock, []interface{}{})
		if err != nil {
			return err
		}

		state := rest.(bool)
		if state {
			fmt.Println("wallet is lock")
		} else {
			fmt.Println("wallet is unlock")
		}

		return nil
	},
}

var walletAddPasswd = &cli.Command{
	Name:  "addpasswd",
	Usage: "add a password for wallet",
	Action: func(cctx *cli.Context) error {
		api, closer, err := GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()
		ctx := ReqContext(cctx)

		if wallet.GetSetupStateForLocal(getWalletRepo(cctx)) {
			fmt.Println("passwd is setup,no need to setup again")
			return nil
		}

		passwd := wallet.Prompt("Enter your password:\n")
		if passwd == "" {
			return xerrors.Errorf("must enter your passwd")
		}
		_, err = api.WalletCustomMethod(ctx, lapi.WalletAddPasswd, []interface{}{passwd, getWalletRepo(cctx)})
		if err != nil {
			fmt.Println(err.Error())
			return err
		}
		fmt.Println("add wallet password success")
		return nil
	},
}

var walletChangePasswd = &cli.Command{
	Name:  "changepasswd",
	Usage: "Change wallet passwd",
	Action: func(cctx *cli.Context) error {
		api, closer, err := GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()
		ctx := ReqContext(cctx)
		if !wallet.GetSetupStateForLocal(getWalletRepo(cctx)) {
			fmt.Println("Passwd is not setup")
			return nil
		}
		// passwd := cctx.String("passwd")
		passwd := wallet.Prompt("Enter your old Password:\n")
		if passwd == "" {
			return xerrors.Errorf("Must enter your old passwd")
		}

		newPasswd := wallet.Prompt("Enter your new Password:\n")
		if passwd == "" {
			return xerrors.Errorf("Must enter your new passwd")
		}
		_, err = api.WalletCustomMethod(ctx, lapi.WalletChangePasswd, []interface{}{passwd, newPasswd})
		if err != nil {
			return err
		}

		fmt.Println("wallet passwd Change success")
		return nil
	},
}

var walletClearPasswd = &cli.Command{
	Name:  "clearpasswd",
	Usage: "Clear wallet passwd",
	Action: func(cctx *cli.Context) error {
		api, closer, err := GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer closer()
		ctx := ReqContext(cctx)
		if !wallet.GetSetupStateForLocal(getWalletRepo(cctx)) {
			fmt.Println("Passwd is not setup")
			return nil
		}
		passwd := wallet.Prompt("Enter your Password:\n")
		if passwd == "" {
			return xerrors.Errorf("Must enter your passwd")
		}
		_, err = api.WalletCustomMethod(ctx, lapi.WalletClearPasswd, []interface{}{passwd})
		if err != nil {
			return err
		}

		fmt.Println("wallet passwd Clear success")
		return nil
	},
}

func getWalletRepo(cctx *cli.Context) string {
	passwdPath, err := homedir.Expand(cctx.String("repo"))
	if err != nil {
		return ""
	}
	return passwdPath + "/keystore/passwd"
}
