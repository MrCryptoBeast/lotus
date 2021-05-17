package sealing

import (
	"context"
	"sort"
	"sync"
	"time"

	miner5 "github.com/filecoin-project/specs-actors/v5/actors/builtin/miner"

	"github.com/ipfs/go-cid"
	"golang.org/x/xerrors"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-bitfield"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/dline"

	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/chain/actors/builtin/miner"
)

var (
	// TODO: config

	PreCommitBatchMax  uint64 = 100 // adjust based on real-world gas numbers, actors limit at 10k
	PreCommitBatchMin  uint64 = 1
	PreCommitBatchWait        = 5 * time.Minute
)

type PreCommitBatcherApi interface {
	StateSectorPartition(ctx context.Context, maddr address.Address, sectorNumber abi.SectorNumber, tok TipSetToken) (*SectorLocation, error)
	SendMsg(ctx context.Context, from, to address.Address, method abi.MethodNum, value, maxFee abi.TokenAmount, params []byte) (cid.Cid, error)
	StateMinerInfo(context.Context, address.Address, TipSetToken) (miner.MinerInfo, error)
	StateMinerProvingDeadline(context.Context, address.Address, TipSetToken) (*dline.Info, error)
	StateMinerPartitions(ctx context.Context, m address.Address, dlIdx uint64, tok TipSetToken) ([]api.Partition, error)
}

type PreCommitBatcher struct {
	api     PreCommitBatcherApi
	maddr   address.Address
	mctx    context.Context
	addrSel AddrSel
	feeCfg  FeeConfig

	todo map[SectorLocation]*bitfield.BitField // MinerSectorLocation -> BitField

	waiting map[abi.SectorNumber][]chan cid.Cid

	notify, stop, stopped chan struct{}
	force                 chan chan *cid.Cid
	lk                    sync.Mutex
}

func NewPreCommitBatcher(mctx context.Context, maddr address.Address, api PreCommitBatcherApi, addrSel AddrSel, feeCfg FeeConfig) *PreCommitBatcher {
	b := &PreCommitBatcher{
		api:     api,
		maddr:   maddr,
		mctx:    mctx,
		addrSel: addrSel,
		feeCfg:  feeCfg,

		todo:    map[SectorLocation]*bitfield.BitField{},
		waiting: map[abi.SectorNumber][]chan cid.Cid{},

		notify:  make(chan struct{}, 1),
		force:   make(chan chan *cid.Cid),
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}

	go b.run()

	return b
}

func (b *PreCommitBatcher) run() {
	var forceRes chan *cid.Cid
	var lastMsg *cid.Cid

	for {
		if forceRes != nil {
			forceRes <- lastMsg
			forceRes = nil
		}
		lastMsg = nil

		var sendAboveMax, sendAboveMin bool
		select {
		case <-b.stop:
			close(b.stopped)
			return
		case <-b.notify:
			sendAboveMax = true
		case <-time.After(PreCommitBatchWait):
			sendAboveMin = true
		case fr := <-b.force: // user triggered
			forceRes = fr
		}

		var err error
		lastMsg, err = b.processBatch(sendAboveMax, sendAboveMin)
		if err != nil {
			log.Warnw("PreCommitBatcher processBatch error", "error", err)
		}
	}
}

func (b *PreCommitBatcher) processBatch(notif, after bool) (*cid.Cid, error) {
	dl, err := b.api.StateMinerProvingDeadline(b.mctx, b.maddr, nil)
	if err != nil {
		return nil, xerrors.Errorf("getting proving deadline info failed: %w", err)
	}

	b.lk.Lock()
	defer b.lk.Unlock()
	params := miner5.PreCommitSectorBatchParams{}

	// TODO: Do the thing
	return &mcid, nil
}

// register precommit, wait for batch message, return message CID
func (b *PreCommitBatcher) AddPreCommit(ctx context.Context, s abi.SectorID) (mcid cid.Cid, terminated bool, err error) {
	// TODO: Do the thing
}

func (b *PreCommitBatcher) Flush(ctx context.Context) (*cid.Cid, error) {
	resCh := make(chan *cid.Cid, 1)
	select {
	case b.force <- resCh:
		select {
		case res := <-resCh:
			return res, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *PreCommitBatcher) Pending(ctx context.Context) ([]abi.SectorID, error) {
	b.lk.Lock()
	defer b.lk.Unlock()

	mid, err := address.IDFromAddress(b.maddr)
	if err != nil {
		return nil, err
	}

	res := make([]abi.SectorID, 0)
	for _, bf := range b.todo {
		err := bf.ForEach(func(id uint64) error {
			res = append(res, abi.SectorID{
				Miner:  abi.ActorID(mid),
				Number: abi.SectorNumber(id),
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	sort.Slice(res, func(i, j int) bool {
		if res[i].Miner != res[j].Miner {
			return res[i].Miner < res[j].Miner
		}

		return res[i].Number < res[j].Number
	})

	return res, nil
}

func (b *PreCommitBatcher) Stop(ctx context.Context) error {
	close(b.stop)

	select {
	case <-b.stopped:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
