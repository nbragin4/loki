package util

import (
	"context"
	"fmt"
	"io"
	"os"

	ot "github.com/opentracing/opentracing-go"

	"github.com/grafana/loki/v3/pkg/storage/stores/series/index"
	"github.com/grafana/loki/v3/pkg/util/math"
)

// DoSingleQuery is the interface for indexes that don't support batching yet.
type DoSingleQuery func(context.Context, index.Query, index.QueryPagesCallback) error

// QueryParallelism is the maximum number of subqueries run in
// parallel per higher-level query
var QueryParallelism = 100

// DoParallelQueries translates between our interface for query batching,
// and indexes that don't yet support batching.
func DoParallelQueries(
	ctx context.Context, doSingleQuery DoSingleQuery, queries []index.Query,
	callback index.QueryPagesCallback,
) error {
	if len(queries) == 1 {
		return doSingleQuery(ctx, queries[0], callback)
	}

	queue := make(chan index.Query)
	incomingErrors := make(chan error)
	n := math.Min(len(queries), QueryParallelism)
	// Run n parallel goroutines fetching queries from the queue
	for i := 0; i < n; i++ {
		go func() {
			sp, ctx := ot.StartSpanFromContext(ctx, "DoParallelQueries-worker")
			defer sp.Finish()
			for {
				query, ok := <-queue
				if !ok {
					return
				}
				incomingErrors <- doSingleQuery(ctx, query, callback)
			}
		}()
	}
	// Send all the queries into the queue
	go func() {
		for _, query := range queries {
			queue <- query
		}
		close(queue)
	}()

	// Now receive all the results.
	var lastErr error
	for i := 0; i < len(queries); i++ {
		err := <-incomingErrors
		if err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// EnsureDirectory makes sure directory is there, if not creates it if not
func EnsureDirectory(dir string) error {
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return os.MkdirAll(dir, 0o777)
	} else if err == nil && !info.IsDir() {
		return fmt.Errorf("not a directory: %s", dir)
	} else if err == nil && info.Mode()&0700 != 0700 {
		return fmt.Errorf("insufficient permissions: %s %s", dir, info.Mode())
	}
	return err
}

// ReadCloserWithContextCancelFunc helps with cancelling the context when closing a ReadCloser.
// NOTE: The consumer of ReadCloserWithContextCancelFunc should always call the Close method when it is done reading which otherwise could cause a resource leak.
type ReadCloserWithContextCancelFunc struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func NewReadCloserWithContextCancelFunc(readCloser io.ReadCloser, cancel context.CancelFunc) io.ReadCloser {
	return ReadCloserWithContextCancelFunc{
		ReadCloser: readCloser,
		cancel:     cancel,
	}
}

func (r ReadCloserWithContextCancelFunc) Close() error {
	defer r.cancel()
	return r.ReadCloser.Close()
}
