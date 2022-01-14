package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	gethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/smartcontractkit/chainlink/core/assets"
	"github.com/smartcontractkit/chainlink/core/internal/gethwrappers/generated/blockhash_store"
	"github.com/smartcontractkit/chainlink/core/scripts/common"
)

// config holds environment variables necessary for this script to run successfully.
type config struct {
	ethURL  string
	chainID int64
	account *bind.TransactOpts
}

func main() {
	config, err := getConfig()
	common.PanicErr(err)

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "not enough arguments to bhsfeeder command")
		os.Exit(1)
	}

	ethClient, err := ethclient.Dial(config.ethURL)
	common.PanicErr(err)

	chainID, err := ethClient.ChainID(context.Background())
	common.PanicErr(err)

	switch os.Args[1] {
	case "latest-blocknumber":
		bn, err := ethClient.BlockNumber(context.Background())
		common.PanicErr(err)
		fmt.Println("latest blocknumber:", bn)
	case "bhs-deploy":
		bhsAddress, tx, _, err := blockhash_store.DeployBlockhashStore(config.account, ethClient)
		common.PanicErr(err)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		fmt.Println("BlockhashStore", bhsAddress.String(), "hash", tx.Hash())
		fmt.Println("Waiting for tx to get mined...")
		_, err = bind.WaitDeployed(ctx, ethClient, tx)
		common.PanicErr(err)
		fmt.Println("BHS Deploy Tx mined")
	case "forward":
		cmd := flag.NewFlagSet("forward", flag.ExitOnError)
		bhsAddress := cmd.String("bhs-address", "", "The address of the deployed blockhash store contract to feed")
		gasPriceGwei := cmd.Int64("gas-price-gwei", -1, "Gas price to start with")
		gasMultiplier := cmd.Float64("gas-multiplier", 1.15, "Gas multiplier to multiply with the gas price to get the final sending gas price")

		common.ParseArgs(cmd, os.Args[2:], "bhs-address", "gas-multiplier", "gas-price-gwei")

		bhs, err := blockhash_store.NewBlockhashStore(gethcommon.HexToAddress(*bhsAddress), ethClient)
		common.PanicErr(err)

		err = feedForward(
			*gasPriceGwei,
			config.account,
			ethClient,
			chainID,
			*gasMultiplier,
			bhs,
		)
		common.PanicErr(err)
	case "backward":
		// do backward
		cmd := flag.NewFlagSet("backward", flag.ExitOnError)
		bhsAddress := cmd.String("bhs-address", "", "The address of the deployed blockhash store contract to feed")
		gasPriceGwei := cmd.Int64("gas-price-gwei", -1, "Gas price to start with")
		gasMultiplier := cmd.Float64("gas-multiplier", 1.15, "Gas multiplier to multiply with the gas price to get the final sending gas price")

		common.ParseArgs(cmd, os.Args[2:], "bhs-address", "gas-multiplier", "gas-price-gwei")

		bhs, err := blockhash_store.NewBlockhashStore(gethcommon.HexToAddress(*bhsAddress), ethClient)
		common.PanicErr(err)

		err = feedBackward(
			*gasPriceGwei,
			config.account,
			ethClient,
			chainID,
			*gasMultiplier,
			bhs,
		)
		common.PanicErr(err)
	case "blockheader":
		cmd := flag.NewFlagSet("blockheader", flag.ExitOnError)
		blockNumber := cmd.Int64("blocknumber", -1, "The blocknumber to get the block header for")

		common.ParseArgs(cmd, os.Args[2:], "blocknumber")

		header, err := serializedBlockHeader(ethClient, big.NewInt(*blockNumber), chainID)
		common.PanicErr(err)

		fmt.Println("Serialized header:", hex.EncodeToString(header))
	case "store-earliest":
		cmd := flag.NewFlagSet("store-earliest", flag.ExitOnError)
		bhsAddress := cmd.String("bhs-address", "", "The address of the deployed blockhash store contract")
		gasPriceGwei := cmd.Int64("gas-price-gwei", -1, "Gas price to start with")

		common.ParseArgs(cmd, os.Args[2:], "bhs-address", "gas-price-gwei")

		bhs, err := blockhash_store.NewBlockhashStore(gethcommon.HexToAddress(*bhsAddress), ethClient)
		common.PanicErr(err)

		config.account.GasPrice = assets.GWei(*gasPriceGwei)

		tx, err := bhs.StoreEarliest(config.account)
		common.PanicErr(err)

		fmt.Println("tx:", tx.Hash(), "waiting for it to mine...")
		receipt, err := bind.WaitMined(context.Background(), ethClient, tx)
		common.PanicErr(err)

		fmt.Println("Confirmed. Blocknumber - 256:", receipt.BlockNumber.Sub(receipt.BlockNumber, big.NewInt(256)))
	case "store":
		cmd := flag.NewFlagSet("store-earliest", flag.ExitOnError)
		bhsAddress := cmd.String("bhs-address", "", "The address of the deployed blockhash store contract")
		gasPriceGwei := cmd.Int64("gas-price-gwei", -1, "Gas price to start with")
		blockNumber := cmd.Int64("blocknumber", -1, "The blocknumber to store the blockhash for")

		common.ParseArgs(cmd, os.Args[2:], "bhs-address", "gas-price-gwei", "blocknumber")

		bhs, err := blockhash_store.NewBlockhashStore(gethcommon.HexToAddress(*bhsAddress), ethClient)
		common.PanicErr(err)

		config.account.GasPrice = assets.GWei(*gasPriceGwei)

		bn := *blockNumber
		for {
			tx, err := bhs.Store(config.account, big.NewInt(bn))
			common.PanicErr(err)

			fmt.Println("tx:", tx.Hash(), "waiting for it to mine...")
			_, err = bind.WaitMined(context.Background(), ethClient, tx)
			common.PanicErr(err)

			fmt.Println("Confirmed")
			time.Sleep(2 * time.Second)
			bn++
		}
	case "get-blockhash":
		cmd := flag.NewFlagSet("get-blockhash", flag.ExitOnError)
		bhsAddress := cmd.String("bhs-address", "", "The address of the deployed blockhash store contract")
		blockNumber := cmd.Int64("blocknumber", -1, "The blocknumber to get the block header for")

		common.ParseArgs(cmd, os.Args[2:], "bhs-address", "blocknumber")

		bhs, err := blockhash_store.NewBlockhashStore(gethcommon.HexToAddress(*bhsAddress), ethClient)
		common.PanicErr(err)

		blockhash, err := bhs.GetBlockhash(nil, big.NewInt(*blockNumber))
		common.PanicErr(err)

		fmt.Println("blockhash found:", gethcommon.Bytes2Hex(blockhash[:]))
	default:
		fmt.Println("Please provide a subcommand, one of 'backward', 'forward', and 'missed'")
	}
}
