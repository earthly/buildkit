package worker

import (
	"github.com/containerd/containerd/filters"
	"github.com/hashicorp/go-multierror"
	"github.com/moby/buildkit/client"
	"github.com/pkg/errors"
)

// Controller holds worker instances.
// Currently, only local workers are supported.
type Controller struct {
	// TODO: define worker interface and support remote ones
	workers []Worker
}

func (c *Controller) Close() error {
	var rerr error
	for _, w := range c.workers {
		if err := w.Close(); err != nil {
			rerr = multierror.Append(rerr, err)
		}
	}
	return rerr
}

// Add adds a local worker.
// The first worker becomes the default.
//
// Add is not thread-safe.
func (c *Controller) Add(w Worker) error {
	c.workers = append(c.workers, w)
	return nil
}

// List lists workers
func (c *Controller) List(filterStrings ...string) ([]Worker, error) {
	filter, err := filters.ParseAll(filterStrings...)
	if err != nil {
		return nil, err
	}
	var workers []Worker
	for _, w := range c.workers {
		if filter.Match(adaptWorker(w)) {
			workers = append(workers, w)
		}
	}
	return workers, nil
}

// GetDefault returns the default local worker
func (c *Controller) GetDefault() (Worker, error) {
	if len(c.workers) == 0 {
		return nil, errors.Errorf("no default worker")
	}
	return c.workers[0], nil
}

func (c *Controller) Get(id string) (Worker, error) {
	for _, w := range c.workers {
		if w.ID() == id {
			return w, nil
		}
	}
	return nil, errors.Errorf("worker %s not found", id)
}

// TODO: add Get(Constraint) (*Worker, error)

// WorkerInfos returns slice of WorkerInfo.
// The first item is the default worker.
func (c *Controller) WorkerInfos() []client.WorkerInfo {
	out := make([]client.WorkerInfo, 0, len(c.workers))
	for _, w := range c.workers {
		taken, size, waiting := w.ParallelismStatus()
		gcSummary, gcCurrent, gcLast := w.GCAnalytics()
		gcAnalytics := client.GCAnalytics{
			NumRuns:            gcSummary.NumRuns,
			NumFailures:        gcSummary.NumFailures,
			AvgDuration:        gcSummary.AvgDuration,
			AvgRecordsCleared:  gcSummary.AvgRecordsCleared,
			AvgSizeCleared:     gcSummary.AvgSizeCleared,
			AvgRecordsBefore:   gcSummary.AvgRecordsBefore,
			AvgSizeBefore:      gcSummary.AvgSizeBefore,
			AllTimeRuns:        gcSummary.AllTimeRuns,
			AllTimeMaxDuration: gcSummary.AllTimeMaxDuration,
			AllTimeDuration:    gcSummary.AllTimeDuration,
		}
		if gcCurrent != nil {
			gcAnalytics.CurrentStartTime = gcCurrent.Start
			gcAnalytics.CurrentNumRecordsBefore = gcCurrent.NumRecordsBefore
			gcAnalytics.CurrentSizeBefore = gcCurrent.SizeBefore
		}
		if gcLast != nil {
			gcAnalytics.LastStartTime = gcLast.Start
			gcAnalytics.LastEndTime = gcLast.End
			gcAnalytics.LastNumRecordsBefore = gcLast.NumRecordsBefore
			gcAnalytics.LastSizeBefore = gcLast.SizeBefore
			gcAnalytics.LastNumRecordsCleared = gcLast.ClearedRecords
			gcAnalytics.LastSizeCleared = gcLast.ClearedSize
			gcAnalytics.LastSuccess = gcLast.Success
		}
		out = append(out, client.WorkerInfo{
			ID:              w.ID(),
			Labels:          w.Labels(),
			Platforms:       w.Platforms(false),
			BuildkitVersion: w.BuildkitVersion(),

			ParallelismCurrent: int(taken),
			ParallelismMax:     int(size),
			ParallelismWaiting: int(waiting),
			GCAnalytics:        gcAnalytics,
		})
	}
	return out
}
