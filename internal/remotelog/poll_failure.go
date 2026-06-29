package remotelog

import (
	"sync"

	"github.com/lich0821/ccNexus/internal/logger"
)

type PollFailureRecorder struct {
	mu        sync.Mutex
	failures  int
	warnAfter int
}

func NewPollFailureRecorder(warnAfter int) *PollFailureRecorder {
	if warnAfter <= 0 {
		warnAfter = 1
	}
	return &PollFailureRecorder{warnAfter: warnAfter}
}

func (r *PollFailureRecorder) Record(err error) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if err == nil {
		if r.failures > 0 {
			if r.failures >= r.warnAfter {
				logger.Info("Remote management poll recovered after %d failures", r.failures)
			}
			r.failures = 0
		}
		return
	}
	r.failures++
	if r.failures < r.warnAfter {
		logger.Debug("Remote management poll failed (%d/%d): %v", r.failures, r.warnAfter, err)
		return
	}
	logger.Warn("Remote management poll failed (%d consecutive): %v", r.failures, err)
}
