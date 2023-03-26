package rediscache

import (
	"encoding/json"
	"fmt"

	"github.com/go-redis/redis"
	"github.com/jatalocks/kube-reqsizer/types"
)

type RedisClient struct {
	Client *redis.Client
}

func (client RedisClient) AddToCache(object types.PodRequests) error {
	val, err := json.Marshal(object)
	if err != nil {
		return fmt.Errorf("failed to add key value to cache error %v", err)
	}
	err = client.Client.Set(object.Name, val, 0).Err()
	if err != nil {
		return fmt.Errorf("failed to add key value to cache error %v", err)
	}
	return nil
}

func (client RedisClient) FetchFromCache(key string) (types.PodRequests, error) {
	// Get the struct value from the cache
	val, err := client.Client.Get(key).Bytes()
	if err != nil {
		return types.PodRequests{}, err
	}
	var req types.PodRequests
	err = json.Unmarshal(val, &req)
	if err != nil {
		return types.PodRequests{}, err
	}
	return req, nil
}

func (client RedisClient) DeleteFromCache(object types.PodRequests) error {
	return client.Client.Del(object.Name).Err()
}

func (client RedisClient) CacheSize() int64 {
	size, err := client.Client.DBSize().Result()
	if err != nil {
		return 0
	}
	return size
}
