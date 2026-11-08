package circuitbreaker_test

import (
	"errors"
	"fmt"
	"time"

	"github.com/birdordev/plumego/x/resilience/circuitbreaker"
)

func ExampleNew() {
	cb := circuitbreaker.New(circuitbreaker.Config{
		Name:             "payment-service",
		FailureThreshold: 0.5,
		SuccessThreshold: 2,
		Timeout:          100 * time.Millisecond,
		MinRequests:      2,
	})

	// Successful call passes through.
	err := cb.Call(func() error { return nil })
	fmt.Println(err)

	// Output:
	// <nil>
}

func ExampleCircuitBreaker_Call_open() {
	cb := circuitbreaker.New(circuitbreaker.Config{
		Name:             "db",
		FailureThreshold: 0.5,
		MinRequests:      2,
		Timeout:          time.Hour,
	})

	backend := errors.New("connection refused")

	// Drive enough failures to open the circuit.
	for range 4 {
		_ = cb.Call(func() error { return backend })
	}

	// Next call should fail fast with ErrOpen.
	err := cb.Call(func() error { return nil })
	fmt.Println(errors.Is(err, circuitbreaker.ErrCircuitOpen))

	// Output:
	// true
}

func ExampleCircuitBreaker_Stats() {
	cb := circuitbreaker.New(circuitbreaker.Config{
		Name:        "cache",
		MinRequests: 10,
	})

	_ = cb.Call(func() error { return nil })
	_ = cb.Call(func() error { return errors.New("miss") })

	stats := cb.Stats()
	fmt.Println(stats.State)
	fmt.Println(stats.Counts.Requests)

	// Output:
	// closed
	// 2
}
