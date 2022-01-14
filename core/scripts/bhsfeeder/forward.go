package main

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/pkg/errors"

	"github.com/smartcontractkit/chainlink/core/assets"
	"github.com/smartcontractkit/chainlink/core/internal/gethwrappers/generated/blockhash_store"
)

const LOOKBACK_BLOCKS uint64 = 5

func feedForward(gasPriceGwei int64,
	account *bind.TransactOpts,
	ethClient *ethclient.Client,
	chainID *big.Int,
	gasMultiplier float64,
	bhs *blockhash_store.BlockhashStore,
) error {
	// Set gas price to ensure non-EIP1559
	account.GasPrice = multiplyGasPrice(assets.GWei(gasPriceGwei), gasMultiplier)

	fmt.Println("Using gas price of", toGwei(account.GasPrice), "gwei to start")

	latestNumber, err := ethClient.BlockNumber(context.Background())
	if err != nil {
		return errors.Wrap(err, "get latest block number")
	}

	fmt.Println("Latest block number:", latestNumber)

	blockNumber := latestNumber - LOOKBACK_BLOCKS
	fmt.Println("feeding forward, starting at block", blockNumber)
	for {
		latestNumber, err := ethClient.BlockNumber(context.Background())
		if err != nil {
			return errors.Wrap(err, "get latest block number")
		}

		batchSize := int(math.Min(float64(BATCH_SIZE), float64(latestNumber)-float64(blockNumber)))
		if batchSize == 0 {
			fmt.Println("caught up with chain tip, going to sleep for 10s")
			time.Sleep(10 * time.Second)
			continue
		}

		fmt.Println("batch size:", batchSize, "latest", latestNumber, "blockNumber", blockNumber, "latest - block", latestNumber-blockNumber)
		txs, nextBlockNumber, err := storeBatch(account, bhs, batchSize, blockNumber)
		if err != nil {
			return errors.Wrap(err, "store batch")
		}
		blockNumber = nextBlockNumber

		if len(txs) > 0 {
			fmt.Println("Confirming", len(txs), "Store() transactions...")
			receipt, err := bind.WaitMined(context.Background(), ethClient, txs[len(txs)-1])
			if err != nil {
				return errors.Wrap(err, "wait for store() receipt")
			}
			fmt.Println("Confirmed txs. Last tx receipt:", receipt)
		}
	}
}

func storeBatch(
	account *bind.TransactOpts,
	bhs *blockhash_store.BlockhashStore,
	batchSize int,
	blockNumber uint64,
) (txs []*types.Transaction, nextBlockNumber uint64, err error) {
	for i := 0; i < batchSize; i++ {
		blocknumToStore := big.NewInt(int64(blockNumber))
		_, err := bhs.GetBlockhash(nil, blocknumToStore)
		if err != nil {
			fmt.Println("blockhash of block number", blocknumToStore.String(), "not in store, storing")
			tx, err := bhs.Store(account, blocknumToStore)
			if err != nil {
				return nil, 0, errors.Wrapf(err, "blockhash store, iteration %d", i)
			}

			fmt.Println("tx hash of Store(", blocknumToStore.String(), "):", tx.Hash())
			txs = append(txs, tx)
		} else {
			fmt.Println("blockhash of block number", blocknumToStore.String(), "already stored")
		}
		blockNumber++
	}
	return txs, blockNumber, nil
}
