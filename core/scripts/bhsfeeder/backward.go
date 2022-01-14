package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/pkg/errors"

	"github.com/smartcontractkit/chainlink/core/assets"
	"github.com/smartcontractkit/chainlink/core/internal/gethwrappers/generated/blockhash_store"
)

const BATCH_SIZE uint64 = 5

// TODO: make more testable
func feedBackward(
	gasPriceGwei int64,
	account *bind.TransactOpts,
	ethClient *ethclient.Client,
	chainID *big.Int,
	gasMultiplier float64,
	bhs *blockhash_store.BlockhashStore,
) error {
	// Set gas price to ensure non-EIP1559
	account.GasPrice = multiplyGasPrice(assets.GWei(gasPriceGwei), gasMultiplier)

	fmt.Println("Using gas price of", toGwei(account.GasPrice), "gwei to start")

	answer := prompt("continue?")
	if answer == "n" {
		return nil
	}

	txEarliest, err := bhs.StoreEarliest(account)
	if err != nil {
		return errors.Wrap(err, "store earliest")
	}

	fmt.Println("tx hash for store earliest", txEarliest.Hash())
	fmt.Println("Waiting for store earliest receipt...")
	receiptEarliest, err := bind.WaitMined(context.Background(), ethClient, txEarliest)
	if err != nil {
		return errors.Wrap(err, "wait for store earliest to mine")
	}

	fmt.Println("receipt for store earliest:", receiptEarliest)

	blockNumber, err := ethClient.BlockNumber(context.Background())
	if err != nil {
		return errors.Wrap(err, "get latest block number")
	}

	txNow, err := bhs.Store(account, new(big.Int).SetInt64(int64(blockNumber)-1))
	if err != nil {
		return errors.Wrap(err, "store head - 1")
	}

	fmt.Println("tx hash for store head - 1", txNow.Hash())

	fmt.Println("waiting for store head - 1 receipt...")
	receiptNow, err := bind.WaitMined(context.Background(), ethClient, txNow)
	if err != nil {
		return errors.Wrap(err, "wait for store head - 1 to mine")
	}

	fmt.Println("receipt for store head - 1:", receiptNow)

	feedFrom := new(big.Int).Set(receiptNow.BlockNumber).Sub(receiptNow.BlockNumber, new(big.Int).SetInt64(256))
	fmt.Println("Sleeping for 20 seconds then feeding blockhashes from block", feedFrom, "backwards")

	time.Sleep(20 * time.Second)
	for {
		// Set gas price to ensure non-EIP1559
		account.GasPrice = multiplyGasPrice(assets.GWei(gasPriceGwei), gasMultiplier)
		fmt.Println("Feeding BlockhashStore at gas price", toGwei(account.GasPrice), "gwei")

		txs := []*types.Transaction{}
		for i := uint64(0); i < BATCH_SIZE; i++ {
			blockNumToStore := new(big.Int).Set(feedFrom).Sub(feedFrom, big.NewInt(1))
			// Call will revert if there is no blockhash for the given block
			blockhash, err := bhs.GetBlockhash(nil, blockNumToStore)
			if err != nil {
				blockHeader, err := serializedBlockHeader(ethClient, feedFrom, chainID)
				if err != nil {
					fmt.Fprintln(os.Stderr, "failed to serialize blockheader:", err)
					return err
				}

				answer := prompt(fmt.Sprintf("go ahead and store header? blocknumber: %s", blockNumToStore.String()))
				if answer == "n" {
					return nil
				}

				tx, err := bhs.StoreVerifyHeader(account, blockNumToStore, blockHeader)
				if err != nil {
					fmt.Fprintln(os.Stderr, "failed to call StoreVerifyHeader:", err)
					return err
				}
				txs = append(txs, tx)
			} else {
				fmt.Println("Blockhash", hex.EncodeToString(blockhash[:]), "already stored for block", blockNumToStore)
			}
			feedFrom.Sub(feedFrom, big.NewInt(1))
		}

		// Check that the txs were mined on-chain.
		// Check only the last one for simplicity.
		receipt, err := bind.WaitMined(context.Background(), ethClient, txs[len(txs)-1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "error waiting for tx to mine:", err)
			return err
		}
		fmt.Println("Batch confirmed. Final Tx Receipt:", receipt)
	}
}
