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
	"time"

	"github.com/redis/go-redis/v9"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Client manages Valkey ACL users for tenant isolation.
type Client interface {
	// UpsertUser creates or replaces a Valkey ACL user restricted to the given prefix.
	UpsertUser(ctx context.Context, username, password string) error
	// DeleteUser removes a Valkey ACL user.
	DeleteUser(ctx context.Context, username string) error
	// UserExists checks whether a Valkey ACL user exists on the master.
	UserExists(ctx context.Context, username string) (bool, error)
	// WatchFailover subscribes to Sentinel "+switch-master" events and calls
	// onFailover whenever a new master is elected. It reconnects automatically
	// on disconnect and returns only when ctx is cancelled. It is a no-op for
	// non-Sentinel clients. Errors are non-fatal — if Sentinel is unreachable
	// the operator continues with its periodic resync.
	WatchFailover(ctx context.Context, onFailover func()) error
	// Close releases the underlying connection.
	Close() error
}

// Config holds the configuration required to connect to Valkey via Sentinel.
type Config struct {
	SentinelAddrs []string
	MasterName    string
	AdminPassword string
}

// sentinelClient provisions ACL users on the Sentinel master AND on every
// known replica. This is necessary because Valkey does not replicate ACL
// commands — each node maintains its own independent ACL table. Without
// provisioning on replicas, pods that connect via the read-replica endpoint
// (LOADER_CACHE_REDIS_READ_URL) will fail authentication once auth is enabled.
type sentinelClient struct {
	// master handles writes and ACL operations via Sentinel leader election.
	master *redis.Client
	// sentinel is used to discover all replica addresses.
	sentinel *redis.SentinelClient
	cfg      Config
}

// NewDirectClient returns a Client with a direct connection to a single Valkey instance.
// Intended for local development and testing — not for production use.
func NewDirectClient(addr, password string) Client {
	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
	})
	return &sentinelClient{master: rdb}
}

// NewSentinelClient returns a Client that provisions ACL users on the Sentinel
// master and all replicas.
func NewSentinelClient(cfg Config) Client {
	master := redis.NewFailoverClient(&redis.FailoverOptions{
		MasterName:    cfg.MasterName,
		SentinelAddrs: cfg.SentinelAddrs,
		Password:      cfg.AdminPassword,
	})
	sentinel := redis.NewSentinelClient(&redis.Options{
		Addr:     cfg.SentinelAddrs[0],
		Password: cfg.AdminPassword,
	})
	return &sentinelClient{master: master, sentinel: sentinel, cfg: cfg}
}

// UpsertUser provisions a per-tenant ACL user on the master and all replicas.
func (c *sentinelClient) UpsertUser(ctx context.Context, username, password string) error {
	if err := c.aclSetUser(ctx, c.master, username, password); err != nil {
		return err
	}
	return c.forEachReplica(ctx, func(rdb *redis.Client) error {
		return c.aclSetUser(ctx, rdb, username, password)
	})
}

// DeleteUser removes a per-tenant ACL user from the master and all replicas.
func (c *sentinelClient) DeleteUser(ctx context.Context, username string) error {
	if err := c.rdo(ctx, c.master, "ACL", "DELUSER", username); err != nil {
		return fmt.Errorf("ACL DELUSER %s on master: %w", username, err)
	}
	return c.forEachReplica(ctx, func(rdb *redis.Client) error {
		if err := c.rdo(ctx, rdb, "ACL", "DELUSER", username); err != nil {
			return fmt.Errorf("ACL DELUSER %s on replica: %w", username, err)
		}
		return nil
	})
}

// UserExists checks for the presence of a Valkey ACL user on the master.
// Replicas are not checked — if the master has the user, replicas should too.
func (c *sentinelClient) UserExists(ctx context.Context, username string) (bool, error) {
	err := c.master.Do(ctx, "ACL", "GETUSER", username).Err()
	if err == nil {
		return true, nil
	}
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if strings.Contains(err.Error(), "No such user") {
		return false, nil
	}
	return false, fmt.Errorf("ACL GETUSER %s: %w", username, err)
}

// WatchFailover subscribes to the Sentinel "+switch-master" pub/sub channel.
// It calls onFailover whenever a master election is detected, then reconnects
// automatically. Safe to call even if Sentinel is temporarily unavailable —
// errors are logged and retried with backoff, never propagated to the caller.
func (c *sentinelClient) WatchFailover(ctx context.Context, onFailover func()) error {
	if c.sentinel == nil {
		return nil // direct client — no Sentinel to watch
	}
	logger := log.FromContext(ctx).WithName("valkey-failover-watch")
	go func() {
		backoff := 2 * time.Second
		for {
			if err := ctx.Err(); err != nil {
				return
			}
			if err := c.subscribeOnce(ctx, onFailover); err != nil {
				logger.Error(err, "Sentinel pub/sub disconnected, retrying", "backoff", backoff)
				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
					if backoff < 2*time.Minute {
						backoff *= 2
					}
				}
			} else {
				backoff = 2 * time.Second // reset on clean exit
			}
		}
	}()
	return nil
}

// subscribeOnce opens one pub/sub session and blocks until the connection drops
// or ctx is cancelled. It calls onFailover for each +switch-master event received.
func (c *sentinelClient) subscribeOnce(ctx context.Context, onFailover func()) error {
	logger := log.FromContext(ctx).WithName("valkey-failover-watch")
	ps := c.sentinel.Subscribe(ctx, "+switch-master")
	defer func() { _ = ps.Close() }()
	logger.Info("Subscribed to Sentinel +switch-master events")
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-ps.Channel():
			if !ok {
				return fmt.Errorf("channel closed")
			}
			logger.Info("Sentinel failover detected, triggering ACL resync",
				"event", msg.Payload)
			onFailover()
		}
	}
}

// FailoverEventHook is called by WatchFailover on each detected failover event.
// Used by main.go to increment the sentinel_failovers_total metric without
// creating a dependency between the valkey and controller packages.
type FailoverEventHook func()

// Close closes all underlying connections.
func (c *sentinelClient) Close() error {
	if c.sentinel != nil {
		_ = c.sentinel.Close()
	}
	return c.master.Close()
}

// aclSetUser issues ACL SETUSER on a single node.
func (c *sentinelClient) aclSetUser(ctx context.Context, rdb *redis.Client, username, password string) error {
	args := []interface{}{
		"ACL", "SETUSER", username,
		"on",
		"resetpass",
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
	if err := rdb.Do(ctx, args...).Err(); err != nil {
		return fmt.Errorf("ACL SETUSER %s: %w", username, err)
	}
	return nil
}

// rdo runs a single Redis command on the given client.
func (c *sentinelClient) rdo(ctx context.Context, rdb *redis.Client, args ...interface{}) error {
	return rdb.Do(ctx, args...).Err()
}

// forEachReplica discovers all current replicas via Sentinel and runs fn on each.
// Errors from individual replicas are logged but do not abort the loop — a
// best-effort approach ensures a single unreachable replica doesn't block provisioning.
func (c *sentinelClient) forEachReplica(ctx context.Context, fn func(*redis.Client) error) error {
	if c.sentinel == nil {
		return nil // direct client — no replicas to discover
	}
	replicas, err := c.sentinel.Replicas(ctx, c.cfg.MasterName).Result()
	if err != nil {
		return fmt.Errorf("sentinel replicas: %w", err)
	}
	var lastErr error
	for _, r := range replicas {
		addr := r["ip"] + ":" + r["port"]
		rdb := redis.NewClient(&redis.Options{
			Addr:     addr,
			Password: c.cfg.AdminPassword,
		})
		if err := fn(rdb); err != nil {
			lastErr = err
		}
		_ = rdb.Close()
	}
	return lastErr
}

// NoopClient is a Client implementation that does nothing, used when Valkey
// configuration is absent (e.g., local development or auth not yet enabled).
type NoopClient struct{}

func (NoopClient) UpsertUser(_ context.Context, _, _ string) error      { return nil }
func (NoopClient) DeleteUser(_ context.Context, _ string) error         { return nil }
func (NoopClient) UserExists(_ context.Context, _ string) (bool, error) { return false, nil }
func (NoopClient) WatchFailover(_ context.Context, _ func()) error      { return nil }
func (NoopClient) Close() error                                         { return nil }
