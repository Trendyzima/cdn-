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

type Entry struct{Key,Path string;Size int64;ExpiresAt,StaleUntil time.Time}
type Cache struct{mu sync.Mutex;root string;maxBytes,bytes int64;items map[string]*list.Element;lru *list.List}
type item struct{e Entry}

func New(root string,maxBytes int64)(*Cache,error){if maxBytes<=0{return nil,errors.New("cache capacity must be positive")};if err:=os.MkdirAll(root,0750);err!=nil{return nil,err};return &Cache{root:root,maxBytes:maxBytes,items:map[string]*list.Element{},lru:list.New()},nil}

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
 if old,ok:=c.items[key];ok{c.detachLocked(old)}
 if err=os.Rename(tmpName,name);err!=nil{return Entry{},err}
 now:=time.Now();e:=Entry{Key:key,Path:name,Size:int64(len(data)),ExpiresAt:now.Add(ttl),StaleUntil:now.Add(ttl+staleFor)}
 c.items[key]=c.lru.PushFront(item{e:e});c.bytes+=e.Size
 for c.bytes>c.maxBytes{c.removeLocked(c.lru.Back())}
 return e,nil
}
func(c *Cache)Delete(key string)bool{c.mu.Lock();defer c.mu.Unlock();el,ok:=c.items[key];if !ok{return false};c.removeLocked(el);return true}
func(c *Cache)Clear(){c.mu.Lock();defer c.mu.Unlock();for c.lru.Len()>0{c.removeLocked(c.lru.Back())}}
func(c *Cache)Stats()(int,int64,int64){c.mu.Lock();defer c.mu.Unlock();return len(c.items),c.bytes,c.maxBytes}
func(c *Cache)detachLocked(el *list.Element){if el==nil{return};e:=el.Value.(item).e;delete(c.items,e.Key);c.bytes-=e.Size;if c.bytes<0{c.bytes=0};c.lru.Remove(el)}
func(c *Cache)removeLocked(el *list.Element){if el==nil{return};e:=el.Value.(item).e;_ = os.Remove(e.Path);delete(c.items,e.Key);c.bytes-=e.Size;if c.bytes<0{c.bytes=0};c.lru.Remove(el)}
func safeName(k string)string{h:=sha256.Sum256([]byte(k));return hex.EncodeToString(h[:])}
