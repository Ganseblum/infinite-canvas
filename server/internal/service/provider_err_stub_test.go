package service

import "github.com/infinite-canvas/server/internal/provider"

type providerErrStub struct{ status int }

func (e *providerErrStub) Error() string { return "stub" }

func (e *providerErrStub) As(target any) bool {
	if upstream, ok := target.(**provider.ErrUpstream); ok {
		*upstream = &provider.ErrUpstream{Status: e.status}
		return true
	}
	return false
}
