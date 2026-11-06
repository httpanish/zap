package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

type transferOp func(Conn, context.Context, func(int)) error

// work repeats requests on one connection until ctx ends. Transport errors and
// server errors are retried after a short backoff, and a rejection ends the run.
func (r *runner) work(ctx context.Context, cancel context.CancelCauseFunc, conn Conn, op transferOp, m *meter, lastErr *atomic.Pointer[error]) {
	defer conn.Close()
	for ctx.Err() == nil {
		err := op(conn, ctx, m.add)
		switch {
		case err == nil:
		case ctx.Err() != nil:
			return
		case errors.Is(err, ErrRejected):
			cancel(err)
			return
		default:
			lastErr.Store(&err)
			sleep(ctx, r.cfg.RetryDelay)
		}
	}
}

// warmPool opens the probe connections before the workers start. Transport
// errors are ignored here, because a later probe redials that connection.
func (r *runner) warmPool(ctx context.Context) ([]Conn, error) {
	wctx, cancel := context.WithTimeout(ctx, r.cfg.PoolWarmTimeout)
	defer cancel()
	pool := make([]Conn, r.cfg.ProbePoolSize)
	errs := make([]error, len(pool))
	var wg sync.WaitGroup
	for i := range pool {
		pool[i] = r.backend.NewConn()
		wg.Go(func() {
			_, errs[i] = pool[i].Probe(wctx)
		})
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return pool, err
	}
	for _, err := range errs {
		if errors.Is(err, ErrRejected) {
			return pool, err
		}
	}
	return pool, nil
}

func closeAll(conns []Conn) {
	for _, c := range conns {
		c.Close()
	}
}
