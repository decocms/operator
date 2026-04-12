/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package valkey

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
)

// Client manages Valkey ACL users for tenant isolation.
type Client interface {
	// UpsertUser creates or replaces a Valkey ACL user restricted to the given prefix.
	UpsertUser(ctx context.Context, username, password string) error
	// DeleteUser removes a Valkey ACL user.
	DeleteUser(ctx context.Context, username string) error
	// UserExists checks whether a Valkey ACL user exists.
	UserExists(ctx context.Context, username string) (bool, error)
	// Close releases the underlying connection.
	Close() error
}

// Config holds the configuration required to connect to Valkey via Sentinel.
type Config struct {
	SentinelAddrs []string
	MasterName    string
	AdminPassword string
}

type sentinelClient struct {
	rdb *redis.Client
}

// NewDirectClient returns a Client with a direct connection to a single Valkey instance.
// Intended for local development and testing — not for production use.
func NewDirectClient(addr, password string) Client {
	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
	})
	return &sentinelClient{rdb: rdb}
}

// NewSentinelClient returns a Client backed by a Sentinel-aware connection.
// The underlying go-redis FailoverClient resolves the current master automatically
// and reconnects after a Sentinel failover.
func NewSentinelClient(cfg Config) Client {
	rdb := redis.NewFailoverClient(&redis.FailoverOptions{
		MasterName:    cfg.MasterName,
		SentinelAddrs: cfg.SentinelAddrs,
		Password:      cfg.AdminPassword,
	})
	return &sentinelClient{rdb: rdb}
}

// UpsertUser issues ACL SETUSER to create or replace the per-tenant user.
// The user is restricted to keys matching <username>:* and lock:<username>:*.
// nocommands resets any prior permission, then we add only what deco needs.
func (c *sentinelClient) UpsertUser(ctx context.Context, username, password string) error {
	args := []interface{}{
		"ACL", "SETUSER", username,
		"on",
		"resetpass", // clear any previously stored password hashes
		">" + password,
		"resetkeys",
		"~" + username + ":*",
		"~lock:" + username + ":*",
		"resetchannels",
		"nocommands",
		"+@read",
		"+@write",
		"+ping",
	}
	if err := c.rdb.Do(ctx, args...).Err(); err != nil {
		return fmt.Errorf("ACL SETUSER %s: %w", username, err)
	}
	return nil
}

// DeleteUser issues ACL DELUSER to remove the per-tenant user.
// Returns nil if the user does not exist.
func (c *sentinelClient) DeleteUser(ctx context.Context, username string) error {
	if err := c.rdb.Do(ctx, "ACL", "DELUSER", username).Err(); err != nil {
		return fmt.Errorf("ACL DELUSER %s: %w", username, err)
	}
	return nil
}

// UserExists checks for the presence of a Valkey ACL user via ACL GETUSER.
func (c *sentinelClient) UserExists(ctx context.Context, username string) (bool, error) {
	err := c.rdb.Do(ctx, "ACL", "GETUSER", username).Err()
	if err == nil {
		return true, nil
	}
	// redis.Nil means the command returned a nil response — treat as not found.
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	// Valkey returns "ERR No such user" for unknown usernames.
	if strings.Contains(err.Error(), "No such user") {
		return false, nil
	}
	return false, fmt.Errorf("ACL GETUSER %s: %w", username, err)
}

// Close closes the underlying Redis connection pool.
func (c *sentinelClient) Close() error {
	return c.rdb.Close()
}

// NoopClient is a Client implementation that does nothing, used when Valkey
// configuration is absent (e.g., local development or auth not yet enabled).
type NoopClient struct{}

func (NoopClient) UpsertUser(_ context.Context, _, _ string) error { return nil }
func (NoopClient) DeleteUser(_ context.Context, _ string) error    { return nil }
func (NoopClient) UserExists(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (NoopClient) Close() error { return nil }
