// Copyright 2018 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package credentialmanager

import (
	"github.com/juju/errors"
	"github.com/juju/loggo"

	"github.com/juju/juju/api/base"
	"github.com/juju/juju/apiserver/params"
)

var logger = loggo.GetLogger("juju.api.credentialmanager")

// Client allows access to the credential management API end point.
type Client struct {
	base.ClientFacade
	facade base.FacadeCaller
}

// NewClient creates a new client for accessing the credential manager API.
func NewClient(st base.APICallCloser) *Client {
	frontend, backend := base.NewClientFacade(st, "CredentialManager")
	return &Client{ClientFacade: frontend, facade: backend}
}

// InvalidateModelCredential invalidates cloud credential for the model that made a connection.
func (c *Client) InvalidateModelCredential(reason string) error {
	var result params.ErrorResult
	err := c.facade.FacadeCall("InvalidateModelCredential", reason, &result)
	if err != nil {
		return errors.Trace(err)
	}
	if result.Error != nil {
		return errors.Trace(result.Error)
	}
	return nil
}
