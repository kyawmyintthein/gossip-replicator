package storage

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"log"
	"sync"

	badger "github.com/dgraph-io/badger/v3"
)

type (
	V struct {
		Data      []byte `json:"data"`
		Version   int    `json:"version"`
		IsDeleted bool   `json:"is_deleted"`
	}

	InMemoryStorage struct {
		mu sync.Mutex

		// useful to share node details with other nodes
		metadata map[string]string

		// node internal state - this is the actual config being gossiped
		db *badger.DB
	}
)

func NewInMemoryDB(md map[string]string) *InMemoryStorage {
	db, err := badger.Open(badger.DefaultOptions("").WithInMemory(true))
	if err != nil {
		log.Fatal(err)
	}
	return &InMemoryStorage{
		metadata: md,
		db:       db,
	}
}

// NodeMeta is used to retrieve meta-data about the current node
// when broadcasting an alive message. It's length is limited to
// the given byte size. This metadata is available in the Node structure.
func (c *InMemoryStorage) NodeMeta(limit int) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()

	var network bytes.Buffer
	encoder := gob.NewEncoder(&network)
	err := encoder.Encode(c.metadata)
	if err != nil {
		log.Fatal("failed to encode metadata", err)
	}
	return network.Bytes()
}

// NotifyMsg is called when a user-data message is received.
// Care should be taken that this method does not block, since doing
// so would block the entire UDP packet receive loop. Additionally, the byte
// slice may be modified after the call returns, so it should be copied if needed
func (c *InMemoryStorage) NotifyMsg(b []byte) {
	// not expecting messages - push/pull sync should suffice
}

// GetBroadcasts is called when user data messages can be broadcast.
// It can return a list of buffers to send. Each buffer should assume an
// overhead as provided with a limit on the total byte size allowed.
// The total byte size of the resulting data to send must not exceed
// the limit. Care should be taken that this method does not block,
// since doing so would block the entire UDP packet receive loop.
func (c *InMemoryStorage) GetBroadcasts(overhead, limit int) [][]byte {
	// nothing to broadcast
	return nil
}

// LocalState is used for a TCP Push/Pull. This is sent to
// the remote side in addition to the membership information. Any
// data can be sent here. See MergeRemoteState as well. The `join`
// boolean indicates this is for a join instead of a push/pull.
func (c *InMemoryStorage) LocalState(join bool) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()

	var network bytes.Buffer
	var iData interface{}
	data := make(map[string][]byte)
	encoder := gob.NewEncoder(&network)
	err := c.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			k := item.Key()
			var vb []byte
			err := item.Value(func(val []byte) error {
				vb = val
				return nil
			})
			if err != nil {
				return nil
			}
			data[string(k)] = vb
		}
		return nil
	})
	if err != nil {
		log.Fatal("failed to encode local state", err)
	}
	iData = data
	err = encoder.Encode(iData)
	if err != nil {
		log.Fatal("failed to encode local state", err)
	}
	return network.Bytes()
}

// MergeRemoteState is invoked after a TCP Push/Pull. This is the
// state received from the remote side and is the result of the
// remote side's LocalState call. The 'join'
// boolean indicates this is for a join instead of a push/pull.
func (c *InMemoryStorage) MergeRemoteState(buf []byte, join bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	network := bytes.NewBuffer(buf)
	decoder := gob.NewDecoder(network)
	data := make(map[string][]byte)
	err := decoder.Decode(&data)
	if err != nil {
		log.Fatal("failed to decode remote state", err)
	}

	log.Println("Received Data from Remote", data)
	for key, value := range data {
		var vin V
		log.Println("Remote data", key, string(value))
		err = json.Unmarshal(value, &vin)
		if err != nil {
			log.Println("invalid input data", err, key, string(value))
			continue
		}

		err := c.db.View(func(txn *badger.Txn) error {
			item, err := txn.Get([]byte(key))
			if err != nil {
				log.Println("get storage error", err, key, string(value))
				if err == badger.ErrKeyNotFound {
					log.Println("not found and append", key, string(value))
					err = c.Put(key, value)
					if err != nil {
						log.Println("put storage error", err, key, string(value))
						return err
					}
					return nil
				}
			}

			var vexit V
			item.Value(func(val []byte) error {
				err = json.Unmarshal(val, &vexit)
				if err != nil {
					log.Println("get storage marshal error", err, key, string(value))
					return err
				}
				return nil
			})

			if vin.Version > vexit.Version {
				log.Println("greater verstion", key, string(value))
				return c.Put(key, value)
			}

			if vin.Version == vexit.Version && (vin.IsDeleted && vexit.IsDeleted) {
				c.Del(key)
				log.Println("same version and deleted", key, string(value))
			}
			return nil
		})
		if err != nil {
			log.Println("db error", err)
		}
	}
	log.Println("successfully merged remote state.")
}

// Put adds config property to config store
func (c *InMemoryStorage) Put(key string, value []byte) error {
	err := c.db.Update(func(txn *badger.Txn) error {
		err := txn.Set([]byte(key), value)
		return err
	})
	return err
}

// Get returns a property value
func (c *InMemoryStorage) Get(key string) ([]byte, error) {
	var data []byte
	err := c.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key))
		if err != nil {
			return err
		}

		item.Value(func(val []byte) error {
			data = append([]byte{}, val...)
			return nil
		})
		return nil
	})
	if err != nil {
		return data, err
	}
	return data, nil
}

// Get returns a property value
func (c *InMemoryStorage) Del(key string) error {
	err := c.db.View(func(txn *badger.Txn) error {
		err := txn.Delete([]byte(key))
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}
