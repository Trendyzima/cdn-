package config

import ("os";"strconv";"strings";"time")

type Config struct {
    ListenAddr string
    OriginURL string
    OriginURLs []string
    ShieldURLs []string
    EdgeURLs []string
    OriginAuthToken string
    PlaybackSecret string
    PurgeToken string
    CacheDir string
    CacheTTL time.Duration
    MaxCacheBytes int64
    HotCacheBytes int64
    SegmentTTL time.Duration
    ManifestTTL time.Duration
    StaleIfError time.Duration
    MaxSegmentBytes int64
    AllowedOrigins []string
    NodeID string
    RateLimitPerMin int
    RateLimitBurst int
    OriginTimeout time.Duration
    MaxIdleConns int
    MaxIdleConnsPerHost int
    MaxConnsPerHost int
    TVMaxRedirects int
}

func Load() Config {
    return Config{
        ListenAddr: env("LISTEN_ADDR",":8080"),
        OriginURL: strings.TrimRight(os.Getenv("ORIGIN_URL"),"/"),
        OriginURLs: split(os.Getenv("ORIGIN_URLS")),
        ShieldURLs: split(os.Getenv("SHIELD_URLS")),
        EdgeURLs: split(os.Getenv("EDGE_URLS")),
        OriginAuthToken: os.Getenv("ORIGIN_AUTH_TOKEN"),
        PlaybackSecret: os.Getenv("PLAYBACK_SECRET"),
        PurgeToken: os.Getenv("PURGE_TOKEN"),
        CacheDir: env("CACHE_DIR","./cache"),
        CacheTTL: duration("CACHE_TTL",30*time.Second),
        MaxCacheBytes: integer("MAX_CACHE_BYTES",16<<30),
        HotCacheBytes: integer("HOT_CACHE_BYTES",512<<20),
        SegmentTTL: duration("SEGMENT_TTL",20*time.Second),
        ManifestTTL: duration("MANIFEST_TTL",2*time.Second),
        StaleIfError: duration("STALE_IF_ERROR",30*time.Second),
        MaxSegmentBytes: integer("MAX_SEGMENT_BYTES",16<<20),
        AllowedOrigins: split(os.Getenv("ALLOWED_ORIGINS")),
        NodeID: env("NODE_ID","local"),
        RateLimitPerMin: int(integer("RATE_LIMIT_PER_MIN",1200)),
        RateLimitBurst: int(integer("RATE_LIMIT_BURST",300)),
        OriginTimeout: duration("ORIGIN_TIMEOUT",10*time.Second),
        MaxIdleConns: int(integer("MAX_IDLE_CONNS",4096)),
        MaxIdleConnsPerHost: int(integer("MAX_IDLE_CONNS_PER_HOST",1024)),
        MaxConnsPerHost: int(integer("MAX_CONNS_PER_HOST",0)),
        TVMaxRedirects: int(integer("TV_MAX_REDIRECTS",5)),
    }
}
func env(k,f string)string{if v:=os.Getenv(k);v!=""{return v};return f}
func duration(k string,f time.Duration)time.Duration{if v:=os.Getenv(k);v!=""{if d,e:=time.ParseDuration(v);e==nil&&d>0{return d}};return f}
func integer(k string,f int64)int64{if v:=os.Getenv(k);v!=""{if n,e:=strconv.ParseInt(v,10,64);e==nil&&n>0{return n}};return f}
func split(v string)[]string{if v==""{return nil};p:=strings.Split(v,",");out:=make([]string,0,len(p));for _,x:=range p{if x=strings.TrimSpace(x);x!=""{out=append(out,strings.TrimRight(x,"/"))}};return out}
