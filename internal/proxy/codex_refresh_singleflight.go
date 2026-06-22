package proxy

import (
	"sync"

	"github.com/lich0821/ccNexus/internal/storage"
)

type credentialRefreshCall struct {
	credential *storage.EndpointCredential
	err        error
	done       chan struct{}
}

func (p *Proxy) startCredentialRefresh(credentialID int64) (*credentialRefreshCall, bool) {
	if p == nil || credentialID <= 0 {
		return &credentialRefreshCall{done: closedRefreshDone()}, false
	}

	p.credentialRefreshMu.Lock()
	defer p.credentialRefreshMu.Unlock()

	if call, ok := p.credentialRefreshCalls[credentialID]; ok {
		return call, true
	}

	call := &credentialRefreshCall{done: make(chan struct{})}
	p.credentialRefreshCalls[credentialID] = call
	return call, false
}

func (p *Proxy) finishCredentialRefresh(credentialID int64, call *credentialRefreshCall) {
	if p == nil || credentialID <= 0 || call == nil {
		return
	}

	p.credentialRefreshMu.Lock()
	if current, ok := p.credentialRefreshCalls[credentialID]; ok && current == call {
		delete(p.credentialRefreshCalls, credentialID)
	}
	p.credentialRefreshMu.Unlock()

	close(call.done)
}

var closedRefreshDoneOnce sync.Once
var closedRefreshDoneChan chan struct{}

func closedRefreshDone() chan struct{} {
	closedRefreshDoneOnce.Do(func() {
		closedRefreshDoneChan = make(chan struct{})
		close(closedRefreshDoneChan)
	})
	return closedRefreshDoneChan
}
