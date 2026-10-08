package redis

import (
 "bytes"
 "encoding/json"
 "fmt"
 "net/http"
 "strings"
 "time"
)

type Client struct{base,token string; http *http.Client}
func New(base,token string)*Client{if strings.TrimSpace(base)==""||strings.TrimSpace(token)==""{return nil};return &Client{strings.TrimRight(base,"/"),token,&http.Client{Timeout:2*time.Second}}}
func(c *Client)cmd(args ...any)(any,error){
 b,e:=json.Marshal(args);if e!=nil{return nil,e}
 req,e:=http.NewRequest(http.MethodPost,c.base,bytes.NewReader(b));if e!=nil{return nil,e}
 req.Header.Set("Authorization","Bearer "+c.token);req.Header.Set("Content-Type","application/json")
 resp,e:=c.http.Do(req);if e!=nil{return nil,e};defer resp.Body.Close()
 if resp.StatusCode<200||resp.StatusCode>=300{return nil,fmt.Errorf("redis REST status %d",resp.StatusCode)}
 var out struct{Result any; Error string}
 if e=json.NewDecoder(resp.Body).Decode(&out);e!=nil{return nil,e};if out.Error!=""{return nil,fmt.Errorf("redis: %s",out.Error)}
 return out.Result,nil
}
func(c *Client)SetNX(key,value string,ttl time.Duration)bool{r,e:=c.cmd("SET",key,value,"NX","PX",ttl.Milliseconds());if e!=nil{return false};return r=="OK"}
func(c *Client)Del(key string){_,_=c.cmd("DEL",key)}
