package db

import (
	"strings"

	"kubesage/internal/config"

	"github.com/redis/go-redis/v9"
)

func NewRedis(cfg config.RedisConfig) redis.UniversalClient {
	addrs := cfg.Addresses
	if len(addrs) == 0 && strings.TrimSpace(cfg.Address) != "" {
		addrs = []string{cfg.Address}
	}
	if len(addrs) == 0 {
		addrs = []string{"127.0.0.1:6379"}
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	switch mode {
	case "cluster":
		return redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:    addrs,
			Username: cfg.Username,
			Password: cfg.Password,
		})
	case "sentinel":
		return redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:       cfg.SentinelMaster,
			SentinelAddrs:    addrs,
			Username:         cfg.Username,
			Password:         cfg.Password,
			SentinelUsername: cfg.SentinelUsername,
			SentinelPassword: cfg.SentinelPassword,
			DB:               cfg.DB,
		})
	default:
		return redis.NewClient(&redis.Options{
			Addr:     addrs[0],
			Username: cfg.Username,
			Password: cfg.Password,
			DB:       cfg.DB,
		})
	}
}
