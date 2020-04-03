package service

import (
	"sync"
)

const (
	// StatusUndefined when service bus can not find the service.
	StatusUndefined = iota

	// StatusInactive when service has been registered in container.
	StatusInactive

	// StatusOK when service has been properly configured.
	StatusOK

	// StatusServing when service is currently done.
	StatusServing

	// StatusStopping when service is currently stopping.
	StatusStopping

	// StatusStopped when service being stopped.
	StatusStopped
)

type service struct {
	name   string
	svc    interface{}
	mu     sync.Mutex
	status int
}

func (e *service) getStatus() int {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.status
}

func (e *service) setStatus(status int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.status = status
}

func (e *service) hasStatus(status int) bool {
	return e.getStatus() == status
}

func (e *service) canServe() bool {
	_, ok := e.svc.(Service)

	return ok
}
