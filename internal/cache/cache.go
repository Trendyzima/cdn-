package cache

import (
    "container/list"
    "errors"
    "os"
    "path/filepath"
    "sync"
    "time"
)

var ErrMiss = errors.New("cache miss")

type Entry struct {
    Key       string
    Path      string
    Size      int64
    ExpiresAt time.Time
}

type Cache struct {
    mu        sync.Mutex
    root      string
    maxBytes  int64
    bytes     int64
    items     map[string]*list.Element
    lru       *list.List
}

type item struct { e Entry }

func New(root string, maxBytes int64) (*Cache, error) {
    if err := os.MkdirAll(root, 0750); err != nil { return nil, err }
    return &Cache{root: root, maxBytes: maxBytes, items: map[string]*list.Element{}, lru: list.New()}, nil
}

func (c *Cache) Get(key string) (Entry, error) {
    c.mu.Lock(); defer c.mu.Unlock()
    el, ok := c.items[key]
    if !ok { return Entry{}, ErrMiss }
    e := el.Value.(item).e
    if time.Now().After(e.ExpiresAt) {
        c.removeLocked(el)
        return Entry{}, ErrMiss
    }
    c.lru.MoveToFront(el)
    return e, nil
}

func (c *Cache) Put(key string, data []byte, ttl time.Duration) (Entry, error) {
    c.mu.Lock(); defer c.mu.Unlock()
    name := filepath.Join(c.root, safeName(key))
    if old, ok := c.items[key]; ok { c.removeLocked(old) }
    if int64(len(data)) > c.maxBytes { return Entry{}, errors.New("object exceeds cache capacity") }
    if err := os.WriteFile(name, data, 0640); err != nil { return Entry{}, err }
    e := Entry{Key:key, Path:name, Size:int64(len(data)), ExpiresAt:time.Now().Add(ttl)}
    el := c.lru.PushFront(item{e:e}); c.items[key]=el; c.bytes += e.Size
    for c.bytes > c.maxBytes { c.removeLocked(c.lru.Back()) }
    return e, nil
}

func (c *Cache) removeLocked(el *list.Element) {
    if el == nil { return }
    e := el.Value.(item).e
    _ = os.Remove(e.Path)
    delete(c.items, e.Key)
    c.bytes -= e.Size
    c.lru.Remove(el)
}

func safeName(k string) string {
    b := make([]byte, 0, len(k)*2)
    for i:=0; i<len(k); i++ {
        ch:=k[i]
        if (ch>='a'&&ch<='z')||(ch>='A'&&ch<='Z')||(ch>='0'&&ch<='9')||ch=='-'||ch=='_'||ch=='.' { b=append(b,ch) } else { b=append(b,'_') }
    }
    return string(b)
}
