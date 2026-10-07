package cache

import ("testing";"time")

func TestCachePutGetAndExpiry(t *testing.T){
 c,err:=New(t.TempDir(),1024);if err!=nil{t.Fatal(err)}
 if _,err=c.Put("a",[]byte("hello"),50*time.Millisecond);err!=nil{t.Fatal(err)}
 e,err:=c.Get("a");if err!=nil||e.Size!=5{t.Fatalf("get: %+v %v",e,err)}
 time.Sleep(60*time.Millisecond)
 if _,err=c.Get("a");err!=ErrMiss{t.Fatalf("expected miss, got %v",err)}
}
func TestCacheEvictsLRU(t *testing.T){
 c,err:=New(t.TempDir(),8);if err!=nil{t.Fatal(err)}
 if _,err=c.Put("a",[]byte("12345"),time.Minute);err!=nil{t.Fatal(err)}
 if _,err=c.Put("b",[]byte("6789"),time.Minute);err!=nil{t.Fatal(err)}
 if _,err=c.Get("a");err!=nil{t.Fatal(err)}
 if _,err=c.Put("c",[]byte("zzzz"),time.Minute);err!=nil{t.Fatal(err)}
 if _,err=c.Get("b");err!=ErrMiss{t.Fatal("expected b to be evicted")}
 if _,err=c.Get("a");err!=nil{t.Fatal("a should remain hot")}
}
