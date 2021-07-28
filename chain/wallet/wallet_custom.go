package wallet

import (
	"context"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/lotus/api"
	"golang.org/x/xerrors"
)

func (w *LocalWallet) WalletCustomMethod(ctx context.Context, meth api.WalletMethod, args []interface{}) (interface{}, error) {

	switch meth {
	case api.Unknown:
		return nil, xerrors.Errorf("exec method is unknown")
	case api.WalletListEnc:
		return w.WalletListEncryption(ctx)
	case api.WalletExportForEnc:
		if len(args) < 2 {
			return nil, xerrors.Errorf("args must is 2 for exec method, but get args is %v", len(args))
		}
		addr_str := args[0].(string)
		addr, _ := address.NewFromString(addr_str)
		passwd := args[1].(string)
		return w.WalletExportForEnc(ctx, addr, passwd)
	case api.WalletDeleteForEnc:
		if len(args) < 2 {
			return nil, xerrors.Errorf("args must is 2 for exec method, but get args is %v", len(args))
		}
		addr_str := args[0].(string)
		addr, _ := address.NewFromString(addr_str)
		passwd := args[1].(string)
		return nil, w.WalletDeleteForEnc(ctx, addr, passwd)
	case api.WalletLock:
		return nil, w.WalletLock(ctx)
	case api.WalletUnlock:
		if len(args) < 1 {
			return nil, xerrors.Errorf("args must is 1 for exec method, but get args is %v", len(args))
		}
		passwd := args[0].(string)

		return nil, w.WalletUnlock(ctx, passwd)
	case api.WalletIsLock:
		return w.WalletIsLock(ctx)
	case api.WalletAddPasswd:
		if len(args) < 2 {
			return nil, xerrors.Errorf("args must is 2 for exec method, but get args is %v", len(args))
		}
		passwd := args[0].(string)
		path := args[1].(string)
		return nil, w.WalletAddPasswd(ctx, passwd, path)
	case api.WalletChangePasswd:
		if len(args) < 2 {
			return nil, xerrors.Errorf("args must is 2 for exec method, but get args is %v", len(args))
		}
		oldPasswd := args[0].(string)
		newPasswd := args[1].(string)
		return w.WalletChangePasswd(ctx, oldPasswd, newPasswd)
	case api.WalletClearPasswd:
		if len(args) < 1 {
			return nil, xerrors.Errorf("args must is 1 for exec method, but get args is %v", len(args))
		}
		passwd := args[0].(string)

		return w.WalletClearPasswd(ctx, passwd)
	case api.WalletCheckPasswd:
		if len(args) < 1 {
			return nil, xerrors.Errorf("args must is 1 for exec method, but get args is %v", len(args))
		}
		passwd := args[0].(string)
		return w.WalletCheckPasswd(ctx, passwd), nil
	default:
		return nil, xerrors.Errorf("exec method is unknown")
	}
}
