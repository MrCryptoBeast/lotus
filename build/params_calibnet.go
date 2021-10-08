// +build calibnet

package build

import (
	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/lotus/chain/actors/policy"
	miner0 "github.com/filecoin-project/specs-actors/actors/builtin/miner"
	builtin2 "github.com/filecoin-project/specs-actors/v2/actors/builtin"
)

var DrandSchedule = map[abi.ChainEpoch]DrandEnum{
	0: DrandIncentinet,
}

const BootstrappersFile = "calibnet.pi"
const GenesisFile = "calibnet.car"

const UpgradeCreeperHeight = 8000
const UpgradeBreezeHeight = 10000
const BreezeGasTampingDuration = 120
const RcPos = -2640

const UpgradeSmokeHeight = 14000

const UpgradeIgnitionHeight = 18000
const UpgradeRefuelHeight = 22000
const UpgradeAmplifierHeight = 26000
const UpgradeHogwartsHeight = 30000
const UpgradeSiriusHeight = 34000

var UpgradeStableHeight = abi.ChainEpoch(38000)
var UpgradeActorsV2Height = abi.ChainEpoch(10_000_001)

const UpgradeTapeHeight = 10_000_002

// This signals our tentative epoch for mainnet launch. Can make it later, but not earlier.
// Miners, clients, developers, custodians all need time to prepare.
// We still have upgrades and state changes to do, but can happen after signaling timing here.
const UpgradeLiftoffHeight = 10_000_003
const UpgradeKumquatHeight = 10_000_004
const UpgradeCalicoHeight = 10_000_005
const UpgradePersianHeight = UpgradeCalicoHeight + (builtin2.EpochsInHour * 60)

const UpgradeOrangeHeight = UpgradePersianHeight + 1

// 2020-12-22T02:00:00Z
const UpgradeClausHeight = UpgradeOrangeHeight + 1

// 2021-03-04T00:00:30Z
var UpgradeActorsV3Height = abi.ChainEpoch(UpgradeClausHeight + 1)

func init() {
	miner0.UpgradeRcHeight = UpgradeBreezeHeight + RcPos
	miner0.InitialPleFactorHeight = UpgradeAmplifierHeight
	policy.SetConsensusMinerMinPower(abi.NewStoragePower(20 << 30))
	policy.SetSupportedProofTypes(
		abi.RegisteredSealProof_StackedDrg16GiBV1,
		abi.RegisteredSealProof_StackedDrg4GiBV1,
	)

	SetAddressNetwork(address.Testnet)

	Devnet = true

	BuildType = BuildCalibnet
}

const BlockDelaySecs = uint64(builtin2.EpochDurationSeconds)

const PropagationDelaySecs = uint64(6)

// BootstrapPeerThreshold is the minimum number peers we need to track for a sync worker to start
const BootstrapPeerThreshold = 1
