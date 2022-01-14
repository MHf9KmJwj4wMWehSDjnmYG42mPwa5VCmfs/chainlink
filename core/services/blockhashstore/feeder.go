package blockhashstore

import (
	"context"

	"github.com/smartcontractkit/chainlink/core/logger"
)

// Event contains metadata about a VRF randomness request or fulfillment.
type Event struct {
	// ID of the relevant VRF request.
	ID string

	// Block that the request or fulfillment was included in.
	Block uint64
}

// Coordinator defines an interface for fetching request and fulfillment metadata from a VRF
// coordinator.
type Coordinator interface {
	// Requests fetches VRF requests that occurred within the specified blocks.
	Requests(ctx context.Context, fromBlock uint64, toBlock uint64) ([]Event, error)

	// Fulfillments fetches VRF fulfillments that occured since the specified block.
	Fulfillments(ctx context.Context, fromBlock uint64) ([]Event, error)
}

// BHS defines an interface for interacting with a BlockhashStore contract.
type BHS interface {
	// Store the hash associated with blockNum.
	Store(ctx context.Context, blockNum uint64) error

	// IsStored checks whether the hash associated with blockNum is already stored.
	IsStored(ctx context.Context, blockNum uint64) (bool, error)
}

func NewFeeder(
	logger logger.Logger,
	coordinator Coordinator,
	bhs BHS,
	waitBlocks int,
	lookbackBlocks int,
	latestBlock func(ctx context.Context) (uint64, error),
) *Feeder {
	return &Feeder{
		logger:         logger,
		coordinator:    coordinator,
		bhs:            bhs,
		waitBlocks:     waitBlocks,
		lookbackBlocks: lookbackBlocks,
		latestBlock:    latestBlock,
		stored:         make(map[uint64]struct{}),
		lastRunBlock:   0,
	}
}

type Feeder struct {
	logger         logger.Logger
	coordinator    Coordinator
	bhs            BHS
	waitBlocks     int
	lookbackBlocks int
	latestBlock    func(ctx context.Context) (uint64, error)

	stored       map[uint64]struct{}
	lastRunBlock uint64
}

func (f *Feeder) Run(ctx context.Context) {
	latestBlock, err := f.latestBlock(ctx)
	if err != nil {
		f.logger.Errorw("Failed to fetch current block number", "error", err)
		return
	}

	var (
		fromBlock        = latestBlock - uint64(f.lookbackBlocks)
		toBlock          = latestBlock - uint64(f.waitBlocks)
		blockToRequests  = make(map[uint64]map[string]struct{})
		requestIDToBlock = make(map[string]uint64)
	)
	reqs, err := f.coordinator.Requests(ctx, fromBlock, toBlock)
	if err != nil {
		f.logger.Errorw("Failed to fetch VRF requests",
			"error", err,
			"latestBlock", latestBlock,
			"fromBlock", fromBlock,
			"toBlock", toBlock)
		return
	}
	for _, req := range reqs {
		if _, ok := blockToRequests[req.Block]; !ok {
			blockToRequests[req.Block] = make(map[string]struct{})
		}
		blockToRequests[req.Block][req.ID] = struct{}{}
		requestIDToBlock[req.ID] = req.Block
	}

	fuls, err := f.coordinator.Fulfillments(ctx, fromBlock)
	if err != nil {
		f.logger.Errorw("Failed to fetch VRF fulfillments",
			"error", err,
			"latestBlock", latestBlock,
			"fromBlock", fromBlock,
			"toBlock", toBlock)
		return
	}
	for _, ful := range fuls {
		requestBlock, ok := requestIDToBlock[ful.ID]
		if !ok {
			return
		}
		delete(blockToRequests[requestBlock], ful.ID)
	}

	for block, unfulfilledReqs := range blockToRequests {
		if len(unfulfilledReqs) > 0 {
			if _, ok := f.stored[block]; ok {
				// Already stored
				continue
			}
			stored, err := f.bhs.IsStored(ctx, block)
			if err != nil {
				f.logger.Errorw("Failed to check if block is already stored, attempting to store anyway",
					"error", err,
					"block", block)
			} else if stored {
				f.logger.Infof("Blockhash already stored",
					"block", block, "latestBlock", latestBlock)
				continue
			}

			// Block needs to be stored
			err = f.bhs.Store(ctx, block)
			if err != nil {
				f.logger.Errorw("Failed to store block", "error", err, "block", block)
				continue
			}
			f.logger.Infof("Stored blockhash",
				"block", block, "latestBlock", latestBlock)
			f.stored[block] = struct{}{}
		}
	}

	if f.lastRunBlock != 0 {
		// Prune stored, anything older than fromBlock can be discarded
		for block := f.lastRunBlock - uint64(f.lookbackBlocks); block < fromBlock; block++ {
			delete(f.stored, block)
		}
	}
	f.lastRunBlock = latestBlock
}
