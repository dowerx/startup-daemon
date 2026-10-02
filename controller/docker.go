package controller

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// Controller manages container start/stop operations and their in-memory TTL leases.
type Controller struct {
	client.APIClient
	sync.Mutex

	leases  map[string]time.Time
	wake    chan struct{}
	wakeMux sync.Once
	ops     sync.Map // container ID -> *sync.Mutex
}

// LeaseRunning creates leases for all currently running containers that have
// the configured host and TTL labels.
func (c *Controller) LeaseRunning(ctx context.Context, labelPrefix string) error {
	if strings.TrimSpace(labelPrefix) == "" {
		return fmt.Errorf("label prefix must not be empty")
	}

	hostLabel := labelPrefix + ".host"
	ttlLabel := labelPrefix + ".ttl"
	containers, err := c.ContainerList(ctx, client.ContainerListOptions{
		All: true,
		Filters: client.Filters{
			"label": {hostLabel: true},
		},
	})
	if err != nil {
		return err
	}

	for _, summary := range containers.Items {
		if summary.State != container.StateRunning {
			continue
		}

		value, ok := summary.Labels[ttlLabel]
		if !ok || strings.TrimSpace(value) == "" {
			log.Printf("warning: skipping container %s: missing required label %s", summary.ID, ttlLabel)
			continue
		}
		ttl, err := time.ParseDuration(value)
		if err != nil {
			log.Printf("warning: skipping container %s: invalid label %s value %q: %v", summary.ID, ttlLabel, value, err)
			continue
		}

		if err := c.setLease(summary.ID, ttl); err != nil {
			log.Printf("warning: skipping container %s: invalid TTL %q: %v", summary.ID, value, err)
			continue
		}
	}
	c.signalWake()
	return nil
}

// Poke starts or unpauses a container when needed and refreshes its TTL lease.
func (c *Controller) Poke(ctx context.Context, summary container.Summary, ttl time.Duration) error {
	if ttl <= 0 {
		return fmt.Errorf("ttl must be positive")
	}
	if ttl > 24*time.Hour {
		return fmt.Errorf("ttl must not exceed 24 hours")
	}

	op := c.operationLock(summary.ID)
	op.Lock()
	defer op.Unlock()

	switch summary.State {
	case container.StatePaused:
		if _, err := c.ContainerUnpause(ctx, summary.ID, client.ContainerUnpauseOptions{}); err != nil {
			return err
		}
	case container.StateCreated, container.StateExited:
		_, err := c.ContainerStart(ctx, summary.ID, client.ContainerStartOptions{})
		if err != nil {
			return err
		}
	case container.StateDead:
		return fmt.Errorf("container %s is dead and cannot be restarted", summary.ID)
	}

	if err := c.setLease(summary.ID, ttl); err != nil {
		return err
	}
	c.signalWake()

	return nil
}

// setLease records a validated lease deadline for a container.
func (c *Controller) setLease(id string, ttl time.Duration) error {
	if ttl <= 0 {
		return fmt.Errorf("ttl must be positive")
	}
	if ttl > 24*time.Hour {
		return fmt.Errorf("ttl must not exceed 24 hours")
	}

	c.Lock()
	defer c.Unlock()
	if c.leases == nil {
		c.leases = make(map[string]time.Time)
	}
	c.leases[id] = time.Now().Add(ttl)
	return nil
}

// reap waits for leases to expire and stops their containers until ctx ends.
func (c *Controller) reap(ctx context.Context) {
	c.wakeMux.Do(func() {
		c.wake = make(chan struct{}, 1)
	})

	for {
		c.Lock()
		var next time.Time
		for _, deadline := range c.leases {
			if next.IsZero() || deadline.Before(next) {
				next = deadline
			}
		}
		c.Unlock()

		// Recompute the timer when leases change; an hour is the maximum idle wait.
		wait := time.Hour
		if !next.IsZero() {
			wait = max(time.Until(next), 0)
		}

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-c.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			continue
		case <-timer.C:
		}

		now := time.Now()
		c.Lock()
		expired := make([]string, 0)
		for id, deadline := range c.leases {
			if !deadline.After(now) {
				expired = append(expired, id)
				delete(c.leases, id)
			}
		}
		c.Unlock()

		// Do not hold the controller lock while making Docker API calls.
		for _, id := range expired {
			// Serialize lifecycle calls per container, then check that a concurrent
			// poke did not renew its lease while it was being marked expired.
			op := c.operationLock(id)
			op.Lock()
			c.Lock()
			_, refreshed := c.leases[id]
			c.Unlock()
			if refreshed {
				op.Unlock()
				continue
			}

			stopTimeout := 10
			_, err := c.ContainerStop(ctx, id, client.ContainerStopOptions{
				Timeout: &stopTimeout,
			})
			op.Unlock()
			if err != nil {
				log.Printf("failed to stop expired container %s: %v", id, err)
				c.Lock()
				if c.leases == nil {
					c.leases = make(map[string]time.Time)
				}
				if _, stillLeased := c.leases[id]; !stillLeased {
					c.leases[id] = time.Now().Add(time.Minute)
				}
				c.Unlock()
				c.signalWake()
			}
		}
	}
}

// operationLock returns the mutex shared by concurrent operations on one container.
func (c *Controller) operationLock(id string) *sync.Mutex {
	lock := &sync.Mutex{}
	actual, _ := c.ops.LoadOrStore(id, lock)
	return actual.(*sync.Mutex)
}

// Reap runs the lease reaper until ctx is cancelled.
func (c *Controller) Reap(ctx context.Context) {
	c.reap(ctx)
}

// signalWake asks the reaper to recalculate its next deadline without blocking.
func (c *Controller) signalWake() {
	c.wakeMux.Do(func() {
		c.wake = make(chan struct{}, 1)
	})
	select {
	case c.wake <- struct{}{}:
	default:
	}
}
