package cache

import (
	"context"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
)

type RedisConfig struct {
	Required bool
	Addr     string
	Password string
	DB       int
}

func OpenRedis(config RedisConfig) (*redis.Client, error) {
	if strings.TrimSpace(config.Addr) == "" {
		if config.Required {
			return nil, fmt.Errorf("redis addr is required")
		}
		return nil, nil
	}
	client := redis.NewClient(&redis.Options{
		Addr:     config.Addr,
		Password: config.Password,
		DB:       config.DB,
	})
	if err := client.Ping(context.Background()).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return client, nil
}
