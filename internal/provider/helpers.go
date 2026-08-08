package provider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/matt2jog/terraform-provider-turso/internal/client"
)

var objectNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

func parseImportID(id string) (string, string, error) {
	parts := strings.Split(id, "/")
	if len(parts) != 2 || !objectNamePattern.MatchString(parts[0]) || !objectNamePattern.MatchString(parts[1]) {
		return "", "", fmt.Errorf("expected import ID organization/name using lowercase letters, digits, and dashes")
	}
	return parts[0], parts[1], nil
}

func operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 2*time.Minute)
}

func parseSizeLimit(value string) (int64, error) {
	text := strings.ToLower(strings.TrimSpace(value))
	if text == "" {
		return 0, nil
	}
	if result, err := strconv.ParseInt(text, 10, 64); err == nil {
		return result, nil
	}
	matches := regexp.MustCompile(`^([0-9]+)\s*(kb|kib|mb|mib|gb|gib|tb|tib)$`).FindStringSubmatch(text)
	if matches == nil {
		return 0, fmt.Errorf("unsupported Turso size limit format")
	}
	amount, err := strconv.ParseInt(matches[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid Turso size limit")
	}
	multipliers := map[string]int64{
		"kb": 1_000, "kib": 1 << 10,
		"mb": 1_000_000, "mib": 1 << 20,
		"gb": 1_000_000_000, "gib": 1 << 30,
		"tb": 1_000_000_000_000, "tib": 1 << 40,
	}
	if amount > (1<<63-1)/multipliers[matches[2]] {
		return 0, fmt.Errorf("Turso size limit exceeds int64")
	}
	return amount * multipliers[matches[2]], nil
}

func stringSet(ctx context.Context, values []string, diagnostics *diag.Diagnostics) types.Set {
	set, diags := types.SetValueFrom(ctx, types.StringType, values)
	diagnostics.Append(diags...)
	return set
}

func waitForGroupConfiguration(ctx context.Context, apiClient *client.Client, org, name string, deleteProtection bool) (*client.Group, error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		group, err := apiClient.GetGroup(ctx, org, name)
		if err == nil && group.DeleteProtection == deleteProtection {
			return group, nil
		}
		if err != nil && !errors.Is(err, client.ErrNotFound) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for Turso group configuration: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func waitForDatabaseConfiguration(ctx context.Context, apiClient *client.Client, org, name string, sizeLimitBytes int64, checkSize, deleteProtection bool) (*client.Database, *client.DatabaseConfiguration, error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		database, configuration, err := apiClient.GetDatabase(ctx, org, name)
		if err == nil && configuration != nil && configuration.DeleteProtection == deleteProtection {
			sizeMatches := !checkSize
			if checkSize {
				actual, parseErr := parseSizeLimit(configuration.SizeLimit)
				if parseErr != nil {
					return nil, nil, parseErr
				}
				sizeMatches = actual == sizeLimitBytes
			}
			if sizeMatches {
				return database, configuration, nil
			}
		}
		if err != nil && !errors.Is(err, client.ErrNotFound) {
			return nil, nil, err
		}
		select {
		case <-ctx.Done():
			return nil, nil, fmt.Errorf("wait for Turso database configuration: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
