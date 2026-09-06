package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

var ErrNotFound = errors.New("not found")

type Client struct {
	rdb *redis.Client
}

// New creates a new storage client connected to DragonFly/Redis
func New(redisURL string) (*Client, error) {
	if redisURL == "" {
		return nil, errors.New("redis url is empty")
	}

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse redis URL: %w", err)
	}

	rdb := redis.NewClient(opts)
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("unable to ping: %w", err)
	}

	return &Client{
		rdb: rdb,
	}, nil
}

// Close gracefully closes the connection
func (c *Client) Close() error {
	return c.rdb.Close()
}

// Ping checks that the Redis/DragonFly connection is alive.
func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

// Set stores a value with the given key in the specified table.
// The value is JSON-marshaled before storage.
func (c *Client) Set(ctx context.Context, table, id string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal value: %w", err)
	}

	key := BuildKey(table, id)
	if err := c.rdb.Set(ctx, key, data, 0).Err(); err != nil {
		return fmt.Errorf("failed to set key %s: %w", key, err)
	}

	return nil
}

// Get retrieves a value by key from the specified table.
// The value is JSON-unmarshaled into the provided destination.
func (c *Client) Get(ctx context.Context, table, id string, dest any) error {
	key := BuildKey(table, id)

	data, err := c.rdb.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return ErrNotFound
		}
		return fmt.Errorf("failed to get key %s: %w", key, err)
	}

	if err := json.Unmarshal(data, dest); err != nil {
		return fmt.Errorf("failed to unmarshal value: %w", err)
	}

	return nil
}

// Delete removes a value by key from the specified table.
func (c *Client) Delete(ctx context.Context, table, id string) error {
	key := BuildKey(table, id)

	result, err := c.rdb.Del(ctx, key).Result()
	if err != nil {
		return fmt.Errorf("failed to delete key %s: %w", key, err)
	}

	if result == 0 {
		return ErrNotFound
	}

	return nil
}

// Exists checks if a key exists in the specified table.
func (c *Client) Exists(ctx context.Context, table, id string) (bool, error) {
	key := BuildKey(table, id)

	result, err := c.rdb.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("failed to check existence of key %s: %w", key, err)
	}

	return result > 0, nil
}

// List retrieves all values from the specified table.
// The values are JSON-unmarshaled using the provided factory function.
func (c *Client) List(ctx context.Context, table string, factory func() any) ([]any, error) {
	pattern := BuildKey(table, "*")

	var keys []string
	seen := make(map[string]bool)
	var cursor uint64
	for {
		batch, nextCursor, err := c.rdb.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, fmt.Errorf("failed to scan keys for table %s: %w", table, err)
		}
		for _, key := range batch {
			if !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	if len(keys) == 0 {
		return []any{}, nil
	}

	values, err := c.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get values for table %s: %w", table, err)
	}

	results := make([]any, 0, len(values))
	for i, v := range values {
		if v == nil {
			return nil, fmt.Errorf("record disappeared during scan: %s", keys[i])
		}

		data, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("unexpected value type in table %s", table)
		}

		dest := factory()
		if err := json.Unmarshal([]byte(data), dest); err != nil {
			return nil, fmt.Errorf("corrupt record in table %s: %w", table, err)
		}

		results = append(results, dest)
	}

	return results, nil
}

// BuildKey creates a namespaced key from table and id.
func BuildKey(table, id string) string {
	return fmt.Sprintf("%s:%s", table, id)
}
