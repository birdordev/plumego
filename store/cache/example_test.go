package cache_test

import (
	"context"
	"fmt"
	"time"

	"github.com/birdordev/plumego/store/cache"
)

func ExampleMemoryCache_Get() {
	ctx := context.Background()
	c := cache.NewMemoryCache()
	defer c.Close()

	if err := c.Set(ctx, "user:42", []byte("profile"), time.Minute); err != nil {
		panic(err)
	}

	value, err := c.Get(ctx, "user:42")
	if err != nil {
		panic(err)
	}

	fmt.Println(string(value))

	// Output:
	// profile
}
