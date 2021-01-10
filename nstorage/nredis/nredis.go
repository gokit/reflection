package nredis

import (
	"fmt"
	"time"

	"github.com/go-redis/redis"

	"github.com/influx6/npkg/nerror"
	"github.com/influx6/npkg/nstorage"
	"github.com/influx6/npkg/nunsafe"
)

var _ nstorage.ExpirableStore = (*RedisStore)(nil)

// RedisStore implements session management, storage and access using redis as
// underline store.
type RedisStore struct {
	tableName string
	hashList  string
	hashElem  string
	Config    *redis.Options
	Client    *redis.Client
}

// NewRedisStore returns a new instance of a redis store.
func NewRedisStore(tableName string, config redis.Options) (*RedisStore, error) {
	var red RedisStore
	red.tableName = tableName
	red.hashList = tableName + "_keys"
	red.hashElem = tableName + "_item"
	red.Config = &config
	if err := red.createConnection(); err != nil {
		return nil, nerror.WrapOnly(err)
	}
	return &red, nil
}

// FromRedisStore returns a new instance of a RedisStore using giving client.
func FromRedisStore(tableName string, conn *redis.Client) (*RedisStore, error) {
	if status := conn.Ping(); status.Err() != nil {
		return nil, status.Err()
	}

	var red RedisStore
	red.tableName = tableName
	red.hashList = tableName + "_keys"
	red.hashElem = tableName + "_item"
	red.Client = conn
	return &red, nil
}

// createConnection attempts to create a new redis connection.
func (rd *RedisStore) createConnection() error {
	client := redis.NewClient(rd.Config)
	status := client.Ping()
	if err := status.Err(); err != nil {
		return nerror.WrapOnly(err)
	}
	rd.Client = client
	return nil
}

func (rd *RedisStore) Close() error {
	return rd.Client.Close()
}

// getHashKey returns formatted for unique form towards using creating
// efficient hashmaps to contain list of keys.
func (rd *RedisStore) getHashKey(key string) string {
	return fmt.Sprintf("_redis_store_%s_%s", rd.hashList, key)
}

// Keys returns all giving keys of elements within store.
func (rd *RedisStore) Keys() ([]string, error) {
	var nstatus = rd.Client.SMembers(rd.hashList)
	if err := nstatus.Err(); err != nil {
		return nil, nerror.WrapOnly(err)
	}
	return nstatus.Val(), nil
}

// Each runs through all elements for giving store, skipping keys
// in redis who have no data or an empty byte slice.
func (rd *RedisStore) Each(fn nstorage.EachItem) error {
	var nstatus = rd.Client.SMembers(rd.hashList)
	if err := nstatus.Err(); err != nil {
		return nerror.WrapOnly(err)
	}
	for _, item := range nstatus.Val() {
		var gstatus = rd.Client.Get(item)
		if err := gstatus.Err(); err == nil {
			var data = nunsafe.String2Bytes(gstatus.Val())
			if doErr := fn(data, item); doErr != nil {
				if nerror.IsAny(doErr, nstorage.ErrJustStop) {
					return nil
				}
				return doErr
			}
		}
	}
	return nil
}

// EachKeyPrefix returns all matching values within store, if elements found match giving
// count then all values returned.
//
// if an error occurs, the partially collected list of keys and error is returned.
//
// Return nstorage.ErrJustStop if you want to just stop iterating.
func (rd *RedisStore) EachKeyPrefix(prefix string) ([]string, error) {
	return rd.FindPrefixFor(100, prefix)
}

// FindPrefixFor returns all matching values within store, if elements found match giving
// count then all values returned.
//
// if an error occurs, the partially collected list of keys and error is returned.
func (rd *RedisStore) FindPrefixFor(count int64, prefix string) ([]string, error) {
	var cursor uint64
	var keys = make([]string, 0, count)
	var err error
	for {
		var ky []string
		var scanned = rd.Client.Scan(cursor, prefix, count)
		ky, cursor, err = scanned.Result()
		if err != nil {
			return keys, nerror.WrapOnly(err)
		}

		keys = append(keys, ky...)
		if cursor == 0 {
			break
		}
	}
	return keys, nil
}

// Exists returns true/false if giving key exists.
func (rd *RedisStore) Exists(key string) (bool, error) {
	var hashKey = rd.getHashKey(key)
	var nstatus = rd.Client.SIsMember(rd.hashList, hashKey)
	if err := nstatus.Err(); err != nil {
		return false, nerror.WrapOnly(err)
	}
	return nstatus.Val(), nil
}

// exists returns true/false if giving key is set in redis.
func (rd *RedisStore) exists(key string) (bool, error) {
	var hashKey = rd.getHashKey(key)
	var nstatus = rd.Client.Exists(hashKey)
	if err := nstatus.Err(); err != nil {
		return false, nerror.WrapOnly(err)
	}
	return nstatus.Val() == 1, nil
}

// expire expires giving key set from underline hash set.
func (rd *RedisStore) expire(keys []string) error {
	var items = make([]interface{}, len(keys))
	for index, elem := range keys {
		items[index] = elem
	}
	var nstatus = rd.Client.SRem(rd.hashList, items...)
	if err := nstatus.Err(); err != nil {
		return nerror.WrapOnly(err)
	}
	return nil
}

// Save adds giving session into storage using redis as underline store.
func (rd *RedisStore) Save(key string, data []byte) error {
	return rd.SaveTTL(key, data, 0)
}

// SaveTTL adds giving session into storage using redis as underline store, with provided
// expiration.
// Duration of 0 means no expiration.
func (rd *RedisStore) SaveTTL(key string, data []byte, expiration time.Duration) error {
	var hashKey = rd.getHashKey(key)
	var nstatus = rd.Client.SAdd(rd.hashList, hashKey)
	if err := nstatus.Err(); err != nil {
		return nerror.WrapOnly(err)
	}

	var nset = rd.Client.Set(hashKey, data, expiration)
	if err := nset.Err(); err != nil {
		return nerror.WrapOnly(err)
	}
	return nil
}

// Update updates giving key with new data slice with 0 duration.
func (rd *RedisStore) Update(key string, data []byte) error {
	return rd.UpdateTTL(key, data, 0)
}

// UpdateTTL updates giving session stored with giving key. It updates
// the underline data and increases the expiration with provided value.
//
// if expiration is zero then giving value expiration will not be reset but left
// as is.
func (rd *RedisStore) UpdateTTL(key string, data []byte, expiration time.Duration) error {
	var hashKey = rd.getHashKey(key)
	var fstatus = rd.Client.SIsMember(rd.hashList, hashKey)
	if err := fstatus.Err(); err != nil {
		return nerror.WrapOnly(err)
	}
	if !fstatus.Val() {
		return nerror.New("key does not exist")
	}

	if len(data) == 0 {
		return rd.remove(key)
	}

	var nset = rd.Client.Set(hashKey, data, expiration)
	if err := nset.Err(); err != nil {
		return nerror.WrapOnly(err)
	}
	return nil
}

// TTL returns current expiration time for giving key.
func (rd *RedisStore) TTL(key string) (time.Duration, error) {
	var hashKey = rd.getHashKey(key)
	var nstatus = rd.Client.PTTL(hashKey)
	if err := nstatus.Err(); err != nil {
		return 0, nerror.WrapOnly(err)
	}
	if nstatus.Val() < 0 {
		return 0, nil
	}
	return nstatus.Val(), nil
}

// ExtendTTL extends the expiration of a giving key if it exists, the duration is expected to be
// in milliseconds. If expiration value is zero then we consider that you wish to remove the expiration.
func (rd *RedisStore) ExtendTTL(key string, expiration time.Duration) error {
	var hashKey = rd.getHashKey(key)
	var nstatus = rd.Client.PTTL(hashKey)
	if err := nstatus.Err(); err != nil {
		return nerror.WrapOnly(err)
	}

	if nstatus.Val() < 0 {
		return nil
	}

	if expiration == 0 {
		var exstatus = rd.Client.Persist(hashKey)
		return exstatus.Err()
	}

	var newExpiration = expiration + nstatus.Val()
	var exstatus = rd.Client.Expire(hashKey, newExpiration)
	return exstatus.Err()
}

// ResetTTL resets giving expiration value to provided duration.
//
// A duration of zero persists the giving key.
func (rd *RedisStore) ResetTTL(key string, expiration time.Duration) error {
	var hashKey = rd.getHashKey(key)
	var nstatus = rd.Client.PTTL(hashKey)
	if err := nstatus.Err(); err != nil {
		return nerror.WrapOnly(err)
	}

	if nstatus.Val() < 0 {
		return nil
	}

	if expiration == 0 {
		var exstatus = rd.Client.Persist(hashKey)
		return exstatus.Err()
	}

	var exstatus = rd.Client.Expire(hashKey, expiration)
	return exstatus.Err()
}

// Get returns giving session stored with giving key, returning an
// error if not found.
func (rd *RedisStore) Get(key string) ([]byte, error) {
	var hashKey = rd.getHashKey(key)
	var nstatus = rd.Client.Get(hashKey)
	if err := nstatus.Err(); err != nil {
		return nil, nerror.WrapOnly(err)
	}
	return nunsafe.String2Bytes(nstatus.Val()), nil
}

// Remove removes underline key from the redis store after retrieving it and
// returning giving session.
func (rd *RedisStore) Remove(key string) ([]byte, error) {
	var hashKey = rd.getHashKey(key)
	var nstatus = rd.Client.Get(hashKey)
	if err := nstatus.Err(); err != nil {
		return nil, nerror.WrapOnly(err)
	}
	var mstatus = rd.Client.SRem(rd.hashList, hashKey)
	if err := mstatus.Err(); err != nil {
		return nil, nerror.WrapOnly(err)
	}
	var dstatus = rd.Client.Del(hashKey)
	if err := dstatus.Err(); err != nil {
		return nil, nerror.WrapOnly(err)
	}
	return nunsafe.String2Bytes(nstatus.Val()), nil
}

func (rd *RedisStore) remove(key string) error {
	var hashKey = rd.getHashKey(key)
	var dstatus = rd.Client.Del(hashKey)
	return dstatus.Err()
}
