package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

func (c *Client) ListOrganizations(ctx context.Context) ([]Organization, error) {
	var result []Organization
	err := c.request(ctx, http.MethodGet, "/v1/organizations", nil, &result)
	return result, err
}

func (c *Client) GetOrganization(ctx context.Context, slug string) (*Organization, error) {
	organizations, err := c.ListOrganizations(ctx)
	if err != nil {
		return nil, err
	}
	for i := range organizations {
		if organizations[i].Slug == slug {
			return &organizations[i], nil
		}
	}
	return nil, ErrNotFound
}

func (c *Client) ListLocations(ctx context.Context) (map[string]string, error) {
	var result LocationListResponse
	err := c.request(ctx, http.MethodGet, "/v1/locations", nil, &result)
	return result.Locations, err
}

func (c *Client) ListGroups(ctx context.Context, org string) ([]Group, error) {
	var result GroupListResponse
	err := c.request(ctx, http.MethodGet, orgPath(org, "groups"), nil, &result)
	return result.Groups, err
}

func (c *Client) GetGroup(ctx context.Context, org, name string) (*Group, error) {
	var result GroupResponse
	err := c.request(ctx, http.MethodGet, orgPath(org, "groups", name), nil, &result)
	return &result.Group, err
}

func (c *Client) GetGroupConfiguration(ctx context.Context, org, name string) (*GroupConfiguration, error) {
	var result GroupConfiguration
	err := c.request(ctx, http.MethodGet, orgPath(org, "groups", name, "configuration"), nil, &result)
	return &result, err
}

func (c *Client) CreateGroup(ctx context.Context, org, name, location string) (*Group, error) {
	var result GroupResponse
	err := c.request(ctx, http.MethodPost, orgPath(org, "groups"), CreateGroupRequest{Name: name, Location: location}, &result)
	if err != nil {
		return nil, err
	}
	return &result.Group, nil
}

func (c *Client) UpdateGroupConfiguration(ctx context.Context, org, name string, deleteProtection bool) error {
	var result GroupConfiguration
	return c.request(ctx, http.MethodPatch, orgPath(org, "groups", name, "configuration"), GroupConfiguration{DeleteProtection: deleteProtection}, &result)
}

func (c *Client) DeleteGroup(ctx context.Context, org, name string) error {
	return c.request(ctx, http.MethodDelete, orgPath(org, "groups", name), nil, nil)
}

func (c *Client) WaitForGroup(ctx context.Context, org, name string, present bool) (*Group, error) {
	return waitFor(ctx, func() (*Group, error) { return c.GetGroup(ctx, org, name) }, present)
}

func (c *Client) ListDatabases(ctx context.Context, org string) ([]Database, error) {
	var result DatabaseListResponse
	err := c.request(ctx, http.MethodGet, orgPath(org, "databases"), nil, &result)
	return result.Databases, err
}

func (c *Client) GetDatabase(ctx context.Context, org, name string) (*Database, *DatabaseConfiguration, error) {
	var result DatabaseResponse
	err := c.request(ctx, http.MethodGet, orgPath(org, "databases", name), nil, &result)
	if err != nil {
		return nil, nil, err
	}
	configuration, configErr := c.GetDatabaseConfiguration(ctx, org, name)
	if configErr != nil && !errors.Is(configErr, ErrNotFound) {
		return nil, nil, configErr
	}
	if configuration != nil {
		result.Database.DeleteProtection = configuration.DeleteProtection
	}
	return &result.Database, configuration, nil
}

func (c *Client) GetDatabaseConfiguration(ctx context.Context, org, name string) (*DatabaseConfiguration, error) {
	var result DatabaseConfiguration
	err := c.request(ctx, http.MethodGet, orgPath(org, "databases", name, "configuration"), nil, &result)
	return &result, err
}

func (c *Client) CreateDatabase(ctx context.Context, org, name, group, sizeLimit string) (*Database, error) {
	var result DatabaseResponse
	err := c.request(ctx, http.MethodPost, orgPath(org, "databases"), CreateDatabaseRequest{Name: name, Group: group, SizeLimit: sizeLimit}, &result)
	if err != nil {
		return nil, err
	}
	return &result.Database, nil
}

func (c *Client) UpdateDatabaseConfiguration(ctx context.Context, org, name, sizeLimit string, deleteProtection bool) error {
	body := UpdateDatabaseConfigurationRequest{SizeLimit: sizeLimit, DeleteProtection: deleteProtection}
	var result DatabaseConfiguration
	return c.request(ctx, http.MethodPatch, orgPath(org, "databases", name, "configuration"), body, &result)
}

func (c *Client) DeleteDatabase(ctx context.Context, org, name string) error {
	return c.request(ctx, http.MethodDelete, orgPath(org, "databases", name), nil, nil)
}

func (c *Client) WaitForDatabase(ctx context.Context, org, name string, present bool) (*Database, *DatabaseConfiguration, error) {
	deadline := time.NewTicker(500 * time.Millisecond)
	defer deadline.Stop()
	for {
		database, configuration, err := c.GetDatabase(ctx, org, name)
		found := err == nil
		if errors.Is(err, ErrNotFound) {
			found = false
		} else if err != nil {
			return nil, nil, err
		}
		if found == present {
			return database, configuration, nil
		}
		select {
		case <-ctx.Done():
			return nil, nil, fmt.Errorf("wait for Turso database readiness: %w", ctx.Err())
		case <-deadline.C:
		}
	}
}

func waitFor[T any](ctx context.Context, read func() (*T, error), present bool) (*T, error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, err := read()
		found := err == nil
		if errors.Is(err, ErrNotFound) {
			found = false
		} else if err != nil {
			return nil, err
		}
		if found == present {
			return result, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for Turso object readiness: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
