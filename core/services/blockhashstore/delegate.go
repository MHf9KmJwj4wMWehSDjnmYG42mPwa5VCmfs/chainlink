package blockhashstore

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"

	"github.com/smartcontractkit/chainlink/core/chains/evm"
	"github.com/smartcontractkit/chainlink/core/internal/gethwrappers/generated/blockhash_store"
	v1 "github.com/smartcontractkit/chainlink/core/internal/gethwrappers/generated/solidity_vrf_coordinator_interface"
	v2 "github.com/smartcontractkit/chainlink/core/internal/gethwrappers/generated/vrf_coordinator_v2"
	"github.com/smartcontractkit/chainlink/core/logger"
	"github.com/smartcontractkit/chainlink/core/services/job"
	"github.com/smartcontractkit/chainlink/core/services/keystore"
)

var _ job.Service = &service{}

const feederTimeout = 10 * time.Second

type Delegate struct {
	logger logger.Logger
	chains evm.ChainSet
	ks     keystore.Eth
}

func NewDelegate(
	logger logger.Logger,
	chains evm.ChainSet,
	ks keystore.Eth,
) *Delegate {
	return &Delegate{
		logger: logger,
		chains: chains,
		ks:     ks,
	}
}

func (d *Delegate) JobType() job.Type {
	return job.BlockhashStore
}

func (d *Delegate) ServicesForSpec(jb job.Job) ([]job.Service, error) {
	if jb.BlockhashStoreSpec == nil {
		return nil, errors.Errorf(
			"blockhashstore.Delegate expects a BlockhashStoreSpec to be present, got %+v", jb)
	}

	chain, err := d.chains.Get(jb.BlockhashStoreSpec.EVMChainID.ToInt())
	if err != nil {
		return nil, fmt.Errorf(
			"getting chain ID %d: %w", jb.BlockhashStoreSpec.EVMChainID.ToInt(), err)
	}

	keys, err := d.ks.SendingKeys()
	if err != nil {
		return nil, errors.Wrap(err, "getting sending keys")
	}
	fromAddress := keys[0].Address
	if jb.BlockhashStoreSpec.FromAddress != nil {
		fromAddress = *jb.BlockhashStoreSpec.FromAddress
	}

	bhs, err := blockhash_store.NewBlockhashStore(
		jb.BlockhashStoreSpec.BlockhashStoreAddress.Address(), chain.Client())
	if err != nil {
		return nil, errors.Wrap(err, "building BHS")
	}

	var coordinators []Coordinator
	if jb.BlockhashStoreSpec.CoordinatorV1Address != nil {
		c, err := v1.NewVRFCoordinator(
			jb.BlockhashStoreSpec.CoordinatorV1Address.Address(), chain.Client())
		if err != nil {
			return nil, errors.Wrap(err, "building V1 coordinator")
		}
		coordinators = append(coordinators, NewV1Coordinator(c))
	}
	if jb.BlockhashStoreSpec.CoordinatorV2Address != nil {
		c, err := v2.NewVRFCoordinatorV2(
			jb.BlockhashStoreSpec.CoordinatorV2Address.Address(), chain.Client())
		if err != nil {
			return nil, errors.Wrap(err, "building V2 coordinator")
		}
		coordinators = append(coordinators, NewV2Coordinator(c))
	}

	bpBHS, err := NewBulletproofBHS(chain.Config(), fromAddress.Address(), chain.TxManager(), bhs)
	if err != nil {
		return nil, errors.Wrap(err, "building bulletproof bhs")
	}

	log := d.logger.Named("BHS Feeder").With("jobID", jb.ID, "externalJobID", jb.ExternalJobID)
	feeder := NewFeeder(
		log,
		NewMultiCoordinator(coordinators...),
		bpBHS,
		int(jb.BlockhashStoreSpec.WaitBlocks),
		int(jb.BlockhashStoreSpec.LookbackBlocks),
		func(ctx context.Context) (uint64, error) {
			head, err := chain.Client().HeadByNumber(ctx, nil)
			if err != nil {
				return 0, errors.Wrap(err, "getting chain head")
			}
			return uint64(head.Number), nil
		})

	return []job.Service{&service{
		feeder:     feeder,
		pollPeriod: jb.BlockhashStoreSpec.PollPeriod,
		logger:     log,
	}}, nil
}

func (d *Delegate) AfterJobCreated(spec job.Job)  {}
func (d *Delegate) BeforeJobDeleted(spec job.Job) {}

type service struct {
	feeder     *Feeder
	stop       chan struct{}
	pollPeriod time.Duration
	logger     logger.Logger
	parentCtx  context.Context
	cancel     context.CancelFunc
}

func (s *service) Start() error {
	if s.stop != nil {
		return errors.New("service already started")
	}

	s.logger.Infow("Starting BHS feeder")

	ticker := time.NewTicker(s.pollPeriod)
	s.stop = make(chan struct{})
	s.parentCtx, s.cancel = context.WithCancel(context.Background())
	go func() {
		for {
			select {
			case <-ticker.C:
				s.runFeeder()
			case <-s.stop:
				ticker.Stop()
				return
			}
		}
	}()

	return nil
}

func (s service) Close() error {
	s.logger.Infow("Stopping BHS feeder")
	close(s.stop)
	s.cancel()
	return nil
}

func (s *service) runFeeder() {
	s.logger.Debugw("Running BHS feeder")
	ctx, cancel := context.WithTimeout(s.parentCtx, feederTimeout)
	defer cancel()
	s.feeder.Run(ctx)
	s.logger.Debugw("BHS feeder run completed")
}
