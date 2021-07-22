// +build debug 2k

package build

import (
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/lotus/chain/actors/policy"
	builtin2 "github.com/filecoin-project/specs-actors/v2/actors/builtin"
)

const BootstrappersFile = ""
const GenesisFile = ""

const UpgradeCreeperHeight = -1
const UpgradeBreezeHeight = -1
const BreezeGasTampingDuration = 0

const UpgradeSmokeHeight = -1
const UpgradeIgnitionHeight = -2
const UpgradeRefuelHeight = -3
const UpgradeTapeHeight = -4

const UpgradeHogwartsHeight = -5
const UpgradeSiriusHeight = -6

var UpgradeActorsV2Height = abi.ChainEpoch(10_000_001)

const UpgradeLiftoffHeight = 10_000_003

const UpgradeKumquatHeight = 10_000_004
const UpgradeCalicoHeight = 10_000_005
const UpgradePersianHeight = UpgradeCalicoHeight + (builtin2.EpochsInHour * 60)
const UpgradeOrangeHeight = UpgradePersianHeight + 1
const UpgradeClausHeight = UpgradeOrangeHeight + 1

const UpgradeActorsV3Height = abi.ChainEpoch(UpgradeClausHeight + 1)

var DrandSchedule = map[abi.ChainEpoch]DrandEnum{
	0: DrandMainnet,
}

func init() {
	policy.SetSupportedProofTypes(abi.RegisteredSealProof_StackedDrg2KiBV1)
	policy.SetConsensusMinerMinPower(abi.NewStoragePower(2048))
	policy.SetMinVerifiedDealSize(abi.NewStoragePower(256))

	BuildType |= Build2k
}

const BlockDelaySecs = uint64(4)

const PropagationDelaySecs = uint64(1)

// SlashablePowerDelay is the number of epochs after ElectionPeriodStart, after
// which the miner is slashed
//
// Epochs
const SlashablePowerDelay = 20

// Epochs
const InteractivePoRepConfidence = 6

const BootstrapPeerThreshold = 1
