package blockhashstore

import (
	"context"
	"testing"
)

var (
	_ Coordinator = &testCoordinator{}
	_ BHS         = testBHS{}
)

func TestFeeder(t *testing.T) {
	tests := []struct {
		name         string
		requests     []Event
		fulfillments []Event
		wait         int
		lookback     int
		latest       uint64
	}
}

type testCoordinator struct {
	requests     []Event
	fulfillments []Event
}

func (t *testCoordinator) Requests(_ context.Context, fromBlock uint64, toBlock uint64) ([]Event, error) {
	var result []Event
	for _, req := range t.requests {
		if req.Block >= fromBlock && req.Block <= toBlock {
			result = append(result, req)
		}
	}
	return result, nil
}

func (t *testCoordinator) Fulfillments(_ context.Context, fromBlock uint64) ([]Event, error) {
	var result []Event
	for _, ful := range t.fulfillments {
		if ful.Block >= fromBlock {
			result = append(result, ful)
		}
	}
	return result, nil
}

type testBHS map[uint64]struct{}

func (t testBHS) Store(_ context.Context, blockNum uint64) error {
	t[blockNum] = struct{}{}
	return nil
}

func (t testBHS) IsStored(_ context.Context, blockNum uint64) (bool, error) {
	_, ok := t[blockNum]
	return ok, nil
}
