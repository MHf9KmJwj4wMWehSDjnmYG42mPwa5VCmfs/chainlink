package blockhashstore

import (
	"context"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/pkg/errors"
	uuid "github.com/satori/go.uuid"

	"github.com/smartcontractkit/chainlink/core/chains/evm/bulletprooftxmanager"
	evmclient "github.com/smartcontractkit/chainlink/core/chains/evm/client"
	"github.com/smartcontractkit/chainlink/core/internal/gethwrappers/generated/blockhash_store"
	"github.com/smartcontractkit/chainlink/core/services/pg"
)

var _ BHS = &BulletproofBHS{}

type bpBHSConfig interface {
	EvmGasLimitDefault() uint64
}

type BulletproofBHS struct {
	config      bpBHSConfig
	jobID       uuid.UUID
	fromAddress common.Address
	bptxm       bulletprooftxmanager.TxManager
	abi         abi.ABI
	bhs         blockhash_store.BlockhashStoreInterface
}

func NewBulletproofBHS(
	config bpBHSConfig,
	fromAddress common.Address,
	bptxm bulletprooftxmanager.TxManager,
	bhs blockhash_store.BlockhashStoreInterface,
) (*BulletproofBHS, error) {
	bhsABI, err := abi.JSON(strings.NewReader(blockhash_store.BlockhashStoreABI))
	if err != nil {
		// blockhash_store.BlockhashStoreABI is generated code, this should never happen
		return nil, errors.Wrap(err, "building ABI")
	}

	return &BulletproofBHS{
		config:      config,
		fromAddress: fromAddress,
		bptxm:       bptxm,
		abi:         bhsABI,
		bhs:         bhs,
	}, nil
}

func (c *BulletproofBHS) Store(ctx context.Context, blockNum uint64) error {
	payload, err := c.abi.Pack("store", new(big.Int).SetUint64(blockNum))
	if err != nil {
		return errors.Wrap(err, "packing args")
	}

	_, err = c.bptxm.CreateEthTransaction(bulletprooftxmanager.NewTx{
		FromAddress:    c.fromAddress,
		ToAddress:      c.bhs.Address(),
		EncodedPayload: payload,
		GasLimit:       c.config.EvmGasLimitDefault(),

		// Set a queue size of 256. At most we store the blockhash of every block, and only the
		// latest 256 can possibly be stored.
		Strategy: bulletprooftxmanager.NewQueueingTxStrategy(c.jobID, 256),
	}, pg.WithParentCtx(ctx))
	if err != nil {
		return errors.Wrap(err, "creating transaction")
	}

	return nil
}

func (c *BulletproofBHS) IsStored(ctx context.Context, blockNum uint64) (bool, error) {
	_, err := c.bhs.GetBlockhash(&bind.CallOpts{Context: ctx}, big.NewInt(int64(blockNum)))
	if evmErr := evmclient.ExtractRPCError(err); evmErr != nil {
		// Transaction reverted because the blockhash is not stored
		return false, nil
	} else if err != nil {
		return false, errors.Wrap(err, "getting blockhash")
	}
	return true, nil
}
