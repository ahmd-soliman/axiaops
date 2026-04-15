package circuitbreaker

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// State represents the circuit breaker state
type State int

const (
	StateClosed State = iota
	StateOpen
	StateHalfOpen
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// Config holds circuit breaker configuration
type Config struct {
	MaxFailures     int           // Number of failures before opening
	ResetTimeout    time.Duration // Time to wait before trying half-open
	SuccessThreshold int          // Successes needed in half-open to close
}

// DefaultConfig returns sensible defaults for scan operations
func DefaultConfig() Config {
	return Config{
		MaxFailures:     3,
		ResetTimeout:    30 * time.Second,
		SuccessThreshold: 2,
	}
}

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	config       Config
	state        State
	failures     int
	successes    int
	lastFailTime time.Time
	mu           sync.RWMutex
}

// New creates a new circuit breaker with the given config
func New(config Config) *CircuitBreaker {
	return &CircuitBreaker{
		config: config,
		state:  StateClosed,
	}
}

// Execute runs the given function with circuit breaker protection
func (cb *CircuitBreaker) Execute(ctx context.Context, fn func() error) error {
	if !cb.canExecute() {
		return fmt.Errorf("circuit breaker is open")
	}

	err := fn()
	cb.recordResult(err)
	return err
}

// canExecute checks if the circuit breaker allows execution
func (cb *CircuitBreaker) canExecute() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	switch cb.state {
	case StateClosed:
		return true
	case StateOpen:
		// Check if we should transition to half-open
		if time.Since(cb.lastFailTime) > cb.config.ResetTimeout {
			cb.mu.RUnlock()
			cb.mu.Lock()
			// Double-check after acquiring write lock
			if cb.state == StateOpen && time.Since(cb.lastFailTime) > cb.config.ResetTimeout {
				cb.state = StateHalfOpen
				cb.successes = 0
			}
			cb.mu.Unlock()
			cb.mu.RLock()
			return cb.state == StateHalfOpen
		}
		return false
	case StateHalfOpen:
		return true
	default:
		return false
	}
}

// recordResult updates the circuit breaker state based on the result
func (cb *CircuitBreaker) recordResult(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err == nil {
		// Success
		cb.failures = 0
		if cb.state == StateHalfOpen {
			cb.successes++
			if cb.successes >= cb.config.SuccessThreshold {
				cb.state = StateClosed
				cb.successes = 0
			}
		}
	} else {
		// Failure
		cb.failures++
		cb.lastFailTime = time.Now()
		cb.successes = 0

		if cb.state == StateClosed && cb.failures >= cb.config.MaxFailures {
			cb.state = StateOpen
		} else if cb.state == StateHalfOpen {
			cb.state = StateOpen
		}
	}
}

// State returns the current state of the circuit breaker
func (cb *CircuitBreaker) State() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// Stats returns current statistics
func (cb *CircuitBreaker) Stats() (State, int, int) {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state, cb.failures, cb.successes
}