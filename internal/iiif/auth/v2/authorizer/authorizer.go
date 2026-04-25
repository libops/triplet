package authorizer

import (
	"context"
	"net/http"
)

type Request struct {
	ItemID string
	Token  string
}

type Authorizer interface {
	Probe(ctx context.Context, req Request) (int, error)
	Token(ctx context.Context, itemID string, r *http.Request) (string, int, error)
	Logout(ctx context.Context, itemID string, r *http.Request) error
}

type PermitAll struct{}

func (PermitAll) Probe(context.Context, Request) (int, error) {
	return http.StatusOK, nil
}

func (PermitAll) Token(context.Context, string, *http.Request) (string, int, error) {
	return "", 0, nil
}

func (PermitAll) Logout(context.Context, string, *http.Request) error {
	return nil
}
