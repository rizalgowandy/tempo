package ingester

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/weaveworks/common/httpgrpc"

	"github.com/grafana/frigg/pkg/friggpb"
	"github.com/grafana/frigg/pkg/ingester/wal"
	"github.com/grafana/frigg/pkg/util"
)

type traceFingerprint uint64

const queryBatchSize = 128

// Errors returned on Query.
var (
	ErrTraceMissing = errors.New("Trace missing")
)

var (
	tracesCreatedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "frigg",
		Name:      "ingester_traces_created_total",
		Help:      "The total number of traces created per tenant.",
	}, []string{"tenant"})
)

type instance struct {
	tracesMtx sync.Mutex
	traces    map[traceFingerprint]*trace

	blockTracesMtx sync.RWMutex
	headBlock      wal.HeadBlock
	lastBlockCut   time.Time

	instanceID         string
	tracesCreatedTotal prometheus.Counter
	limiter            *Limiter
	wal                wal.WAL
}

func newInstance(instanceID string, limiter *Limiter, wal wal.WAL) (*instance, error) {
	i := &instance{
		traces: map[traceFingerprint]*trace{},

		instanceID:         instanceID,
		tracesCreatedTotal: tracesCreatedTotal.WithLabelValues(instanceID),
		limiter:            limiter,
		wal:                wal,
	}
	err := i.ResetBlock()
	if err != nil {
		return nil, err
	}
	return i, nil
}

func (i *instance) Push(ctx context.Context, req *friggpb.PushRequest) error {
	i.tracesMtx.Lock()
	defer i.tracesMtx.Unlock()

	trace, err := i.getOrCreateTrace(req)
	if err != nil {
		return err
	}

	if err := trace.Push(ctx, req); err != nil {
		return err
	}

	return nil
}

// Moves any complete traces out of the map to complete traces
func (i *instance) CutCompleteTraces(cutoff time.Duration, immediate bool) error {
	i.tracesMtx.Lock()
	defer i.tracesMtx.Unlock()

	i.blockTracesMtx.Lock()
	defer i.blockTracesMtx.Unlock()

	now := time.Now()
	for key, trace := range i.traces {
		if now.Add(cutoff).After(trace.lastAppend) || immediate {
			err := i.headBlock.Write(trace.traceID, trace.trace)
			if err != nil {
				return err
			}

			delete(i.traces, key)
		}
	}

	return nil
}

func (i *instance) IsBlockReady(maxTracesPerBlock int, maxBlockLifetime time.Duration) bool {
	i.blockTracesMtx.RLock()
	defer i.blockTracesMtx.RUnlock()

	if i.headBlock == nil {
		return false
	}

	now := time.Now()
	return i.headBlock.Length() >= maxTracesPerBlock || i.lastBlockCut.Add(maxBlockLifetime).Before(now)
}

// GetBlock() returns complete traces.  It is up to the caller to do something sensible at this point
func (i *instance) GetBlock() wal.HeadBlock {
	return i.headBlock
}

func (i *instance) ResetBlock() error {
	if i.headBlock != nil {
		i.headBlock.Clear()
	}

	var err error
	i.headBlock, err = i.wal.NewBlock(uuid.New(), i.instanceID)
	i.lastBlockCut = time.Now()
	return err
}

func (i *instance) FindTraceByID(id []byte) (*friggpb.Trace, error) {
	i.blockTracesMtx.Lock()
	defer i.blockTracesMtx.Unlock()

	out := &friggpb.Trace{}

	found, err := i.headBlock.Find(id, out)
	if err != nil {
		return nil, err
	}

	if found {
		return out, nil
	}

	return nil, nil
}

func (i *instance) getOrCreateTrace(req *friggpb.PushRequest) (*trace, error) {
	if len(req.Batch.Spans) == 0 {
		return nil, fmt.Errorf("invalid request received with 0 spans")
	}

	// two assumptions here should hold.  distributor separates spans by traceid.  0 length span slices should be filtered before here
	traceID := req.Batch.Spans[0].TraceId
	fp := traceFingerprint(util.Fingerprint(traceID))

	trace, ok := i.traces[fp]
	if ok {
		return trace, nil
	}

	err := i.limiter.AssertMaxTracesPerUser(i.instanceID, len(i.traces))
	if err != nil {
		return nil, httpgrpc.Errorf(http.StatusTooManyRequests, err.Error())
	}

	trace = newTrace(fp, traceID)
	i.traces[fp] = trace
	i.tracesCreatedTotal.Inc()

	return trace, nil
}

func isDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}
