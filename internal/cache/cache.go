package cache

import (
 "container/list"
 "crypto/sha256"
 "encoding/hex"
 "errors"
 "os"
 "path/filepath"
 "sync"
 "time"
)

var ErrMiss = errors.New("cache miss")

type Entry struct{Key,Path string;Size int64;CreatedAt,ExpiresAt,StaleUntil time.Time}
type Cache struct{mu sync.Mutex;root string;maxBytes,bytes int64;hotMaxBytes,hotBytes int64;items map[string]*list.Element;lru *list.List;hot map[string]*list.Element;hotLRU *list.List}
type item struct{e Entry}

func New(root string,maxBytes int64)(*Cache,error){return NewWithHot(root,maxBytes,0)}
func NewWithHot(root string,maxBytes,hotMaxBytes int64)(*Cache,error){if maxBytes<=0{return nil,errors.New("cache capacity must be positive")};if hotMaxBytes<0{return nil,errors.New("hot cache capacity cannot be negative")};if err:=os.MkdirAll(root,0750);err!=nil{return nil,err};return &Cache{root:root,maxBytes:maxBytes,hotMaxBytes:hotMaxBytes,items:map[string]*list.Element{},lru:list.New(),hot:map[string]*list.Element{},hotLRU:list.New()},nil}

func(c *Cache)GetHot(key string)(Entry,[]byte,error){c.mu.Lock();el,ok:=c.hot[key];if !ok{c.mu.Unlock();return Entry{},nil,ErrMiss};h:=el.Value.(hotItem);if !time.Now().Before(h.e.ExpiresAt){c.hotRemoveLocked(el);c.mu.Unlock();return Entry{},nil,ErrMiss};c.hotLRU.MoveToFront(el);e:=h.e;data:=h.data;c.mu.Unlock();return e,data,nil}

func(c *Cache)Get(key string)(Entry,error){
 c.mu.Lock();el,ok:=c.items[key];if !ok{c.mu.Unlock();return Entry{},ErrMiss};e:=el.Value.(item).e;now:=time.Now()
 if !now.Before(e.ExpiresAt){if !now.Before(e.StaleUntil){c.removeLocked(el)};c.mu.Unlock();return Entry{},ErrMiss}
 c.lru.MoveToFront(el);c.mu.Unlock()
 if _,err:=os.Stat(e.Path);err!=nil{c.Delete(key);return Entry{},ErrMiss};return e,nil
}
func(c *Cache)GetStale(key string)(Entry,error){
 c.mu.Lock();el,ok:=c.items[key];if !ok{c.mu.Unlock();return Entry{},ErrMiss};e:=el.Value.(item).e;now:=time.Now()
 if now.Before(e.ExpiresAt)||!now.Before(e.StaleUntil){if !now.Before(e.StaleUntil){c.removeLocked(el)};c.mu.Unlock();return Entry{},ErrMiss}
 c.lru.MoveToFront(el);c.mu.Unlock();if _,err:=os.Stat(e.Path);err!=nil{c.Delete(key);return Entry{},ErrMiss};return e,nil
}

// Put publishes a replacement with an atomic rename. The old pathname is never
// unlinked before the rename, so readers holding the old path cannot observe a
// transient 404 while another request refreshes the same HLS object.
func(c *Cache)Put(key string,data []byte,ttl,staleFor time.Duration)(Entry,error){
 if ttl<=0{return Entry{},errors.New("cache ttl must be positive")};if staleFor<0{return Entry{},errors.New("stale window cannot be negative")};if int64(len(data))>c.maxBytes{return Entry{},errors.New("object exceeds cache capacity")}
 name:=filepath.Join(c.root,safeName(key));tmp,err:=os.CreateTemp(c.root,".cache-*");if err!=nil{return Entry{},err};tmpName:=tmp.Name();defer os.Remove(tmpName)
 if _,err=tmp.Write(data);err!=nil{_ = tmp.Close();return Entry{},err};if err=tmp.Chmod(0640);err!=nil{_ = tmp.Close();return Entry{},err};if err=tmp.Close();err!=nil{return Entry{},err}
 c.mu.Lock();defer c.mu.Unlock()
 if err=os.Rename(tmpName,name);err!=nil{return Entry{},err}
 if old,ok:=c.items[key];ok{c.detachMetadataLocked(old)}
 now:=time.Now();e:=Entry{Key:key,Path:name,Size:int64(len(data)),CreatedAt:now,ExpiresAt:now.Add(ttl),StaleUntil:now.Add(ttl+staleFor)}
 c.items[key]=c.lru.PushFront(item{e:e});c.bytes+=e.Size
 if c.hotMaxBytes>0 && e.Size<=c.hotMaxBytes { c.hotPutLocked(e,data) }
 for c.bytes>c.maxBytes{c.removeLocked(c.lru.Back())}
 return e,nil
}
func(c *Cache)Delete(key string)bool{c.mu.Lock();defer c.mu.Unlock();el,ok:=c.items[key];if !ok{return false};c.removeLocked(el);return true}
func(c *Cache)Clear(){c.mu.Lock();defer c.mu.Unlock();for c.lru.Len()>0{c.removeLocked(c.lru.Back())};for c.hotLRU.Len()>0{c.hotRemoveLocked(c.hotLRU.Back())}}
func(c *Cache)Stats()(int,int64,int64){c.mu.Lock();defer c.mu.Unlock();return len(c.items),c.bytes,c.maxBytes}
func(c *Cache)detachMetadataLocked(el *list.Element){if el==nil{return};e:=el.Value.(item).e;delete(c.items,e.Key);c.bytes-=e.Size;if c.bytes<0{c.bytes=0};if h,ok:=c.hot[e.Key];ok{c.hotRemoveLocked(h)};c.lru.Remove(el)}
func(c *Cache)detachLocked(el *list.Element){if el==nil{return};e:=el.Value.(item).e;_ = os.Remove(e.Path);c.detachMetadataLocked(el)}
func(c *Cache)removeLocked(el *list.Element){if el==nil{return};e:=el.Value.(item).e;_ = os.Remove(e.Path);delete(c.items,e.Key);c.bytes-=e.Size;if c.bytes<0{c.bytes=0};c.lru.Remove(el)}
type hotItem struct{e Entry;data []byte}
func(c *Cache)hotPutLocked(e Entry,data []byte){if old,ok:=c.hot[e.Key];ok{c.hotBytes-=old.Value.(hotItem).e.Size;c.hotLRU.Remove(old);delete(c.hot,e.Key)};h:=c.hotLRU.PushFront(hotItem{e:e,data:data});c.hot[e.Key]=h;c.hotBytes+=e.Size;for c.hotBytes>c.hotMaxBytes{back:=c.hotLRU.Back();if back==nil{break};c.hotRemoveLocked(back)}}
func(c *Cache)hotRemoveLocked(el *list.Element){if el==nil{return};h:=el.Value.(hotItem);delete(c.hot,h.e.Key);c.hotBytes-=h.e.Size;if c.hotBytes<0{c.hotBytes=0};c.hotLRU.Remove(el)}
func(c *Cache)HotStats()(int64,int64){c.mu.Lock();defer c.mu.Unlock();return c.hotBytes,c.hotMaxBytes}

func safeName(k string)string{h:=sha256.Sum256([]byte(k));return hex.EncodeToString(h[:])}
