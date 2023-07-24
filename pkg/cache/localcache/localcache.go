package localcache

import (
	"errors"

	"github.com/jatalocks/kube-reqsizer/types"
	"k8s.io/client-go/tools/cache"
)

func AddToCache(cacheStore cache.Store, object types.PodRequests) error {
	return cacheStore.Add(object)
}

func FetchFromCache(cacheStore cache.Store, key string) (types.PodRequests, error) {
	obj, exists, err := cacheStore.GetByKey(key)
	if err != nil {
		// klog.Errorf("failed to add key value to cache error", err)
		return types.PodRequests{}, err
	}
	if !exists {
		// klog.Errorf("object does not exist in the cache")
		err = errors.New("object does not exist in the cache")
		return types.PodRequests{}, err
	}
	return obj.(types.PodRequests), nil
}

func DeleteFromCache(cacheStore cache.Store, object types.PodRequests) error {
	return cacheStore.Delete(object)
}
