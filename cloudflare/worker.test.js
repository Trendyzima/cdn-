import test from "node:test";
import assert from "node:assert/strict";
import { createHmac } from "node:crypto";
import worker from "./worker.js";

const secret = "0123456789abcdef0123456789abcdef";
const key = "users/u1/0123456789abcdef0123456789abcdef";
function assertion({objectKey=key,subject="u1",owner,visibility="private",exp=Math.floor(Date.now()/1000)+120,iat=Math.floor(Date.now()/1000)}={}) {
  const claims={v:"v1",iss:"testagram-backend",aud:"testagram-cdn",sub:subject,...(owner?{owner}:{}),key:objectKey,visibility,iat,exp,jti:"abcdef0123456789abcdef0123456789"};
  const payload=Buffer.from(JSON.stringify(claims)).toString("base64url");
  const input="v1."+payload;
  return input+"."+createHmac("sha256",secret).update(input).digest("base64url");
}
function makeObject(body,type="image/webp",rangeValue) {
  const bytes=new TextEncoder().encode(body);
  return {body:new ReadableStream({start(c){c.enqueue(bytes);c.close();}}),size:bytes.byteLength,httpEtag:'"test-etag"',range:rangeValue,writeHttpMetadata(h){h.set("content-type",type);}};
}
function request(path="/v1/"+key,token=assertion(),headers={}) {
  return new Request("https://media.testagram.site"+path,{headers:{Authorization:"Bearer "+token,...headers}});
}
function env(get=async()=>makeObject("private-media")) {
  return {CDN_MEDIA_ASSERTION_SECRET:secret,MEDIA_BUCKET:{get}};
}

test("serves only Backend-issued object- and viewer-bound assertions",async()=>{
  const res=await worker.fetch(request(),env(async requested=>{assert.equal(requested,key);return makeObject("private-media");}));
  assert.equal(res.status,200);assert.equal(res.headers.get("cache-control"),"private, no-store");assert.equal(await res.text(),"private-media");
  assert.equal(res.headers.get("x-testagram-cdn-version"),"2026-11-12-r2-backend-assertion-v1");
});
test("rejects missing, expired, wrong-key, wrong-viewer and public assertions before R2",async()=>{
  let reads=0;const e=env(async()=>{reads++;return makeObject("should-not-read");});
  const invalid=[
    request("/v1/"+key,""),
    request("/v1/"+key,assertion({exp:Math.floor(Date.now()/1000)-1})),
    request("/v1/"+key,assertion({objectKey:"users/u1/ffffffffffffffffffffffffffffffff"})),
    request("/v1/"+key,assertion({subject:"u2"})),
    request("/v1/"+key,assertion({visibility:"public"})),
    request("/v1/"+key,assertion({exp:Math.floor(Date.now()/1000)+600})),
  ];
  for(const req of invalid){const res=await worker.fetch(req,e);assert.equal(res.status,401);}
  assert.equal(reads,0);
});
test("rejects tampered claims and query-string tokens",async()=>{
  let reads=0;const e=env(async()=>{reads++;return makeObject("x");});
  const token=assertion();const tampered=token.slice(0,-1)+(token.endsWith("A")?"B":"A");
  assert.equal((await worker.fetch(request("/v1/"+key,tampered),e)).status,401);
  const q=await worker.fetch(new Request("https://media.testagram.site/v1/"+key+"?token="+token),e);
  assert.equal(q.status,400);assert.equal(reads,0);
});
test("private byte ranges preserve 206 and no-store semantics",async()=>{
  const e=env(async(_key,options)=>{assert.deepEqual(options.range,{offset:0,length:4});return {...makeObject("test","video/mp4",{offset:0,length:4}),size:10};});
  const res=await worker.fetch(request("/v1/"+key,assertion(),{Range:"bytes=0-3"}),e);
  assert.equal(res.status,206);assert.equal(res.headers.get("content-range"),"bytes 0-3/10");assert.equal(res.headers.get("cache-control"),"private, no-store");
});
test("missing and weak assertion secrets fail closed without reading R2",async()=>{
  let reads=0;const bucket={get:async()=>{reads++;return makeObject("x");}};
  for(const e of [{MEDIA_BUCKET:bucket},{CDN_MEDIA_ASSERTION_SECRET:"weak",MEDIA_BUCKET:bucket}]){
    const res=await worker.fetch(request(),e);assert.equal(res.status,401);
  }
  assert.equal(reads,0);
});
test("path traversal never reaches R2",async()=>{
  let reads=0;const res=await worker.fetch(new Request("https://media.testagram.site/v1/users/u1/../secret.webp"),{CDN_MEDIA_ASSERTION_SECRET:secret,MEDIA_BUCKET:{get:async()=>{reads++;return null;}}});
  assert.equal(res.status,404);assert.equal(reads,0);
});
test("health and readiness expose Worker identity",async()=>{
  const e={CDN_MEDIA_ASSERTION_SECRET:secret,MEDIA_BUCKET:{list:async()=>({objects:[]})}};
  const health=await worker.fetch(new Request("https://media.testagram.site/healthz"),e);
  const ready=await worker.fetch(new Request("https://media.testagram.site/readyz"),e);
  assert.equal(health.status,200);assert.equal(ready.status,200);
  assert.equal(health.headers.get("x-testagram-cdn-version"),"2026-11-12-r2-backend-assertion-v1");
});

test("accepts Appwrite owner IDs containing underscores and periods",async()=>{
  const objectKey="users/user_123.test/0123456789abcdef0123456789abcdef";
  const token=assertion({objectKey,subject:"user_123.test"});
  const res=await worker.fetch(request("/v1/"+objectKey,token),env(async requested=>{
    assert.equal(requested,objectKey);
    return makeObject("appwrite-owner-media");
  }));
  assert.equal(res.status,200);
  assert.equal(await res.text(),"appwrite-owner-media");
});

test("accepts viewer-bound assertions for another owner's media",async()=>{
  const objectKey="users/creator_1/0123456789abcdef0123456789abcdef";
  const token=assertion({objectKey,subject:"viewer_2",owner:"creator_1"});
  const res=await worker.fetch(request("/v1/"+objectKey,token),env(async requested=>{
    assert.equal(requested,objectKey);
    return makeObject("authorized-followed-media");
  }));
  assert.equal(res.status,200);
  assert.equal(await res.text(),"authorized-followed-media");
});


test("readiness fails when the assertion secret is missing or weak",async()=>{
  const bucket={list:async()=>({objects:[]})};
  for(const secretValue of [undefined,"weak"]){
    const e={MEDIA_BUCKET:bucket};
    if(secretValue!==undefined)e.CDN_MEDIA_ASSERTION_SECRET=secretValue;
    const ready=await worker.fetch(new Request("https://media.testagram.site/readyz"),e);
    assert.equal(ready.status,503);
    const body=await ready.json();
    assert.equal(body.ok,false);
    assert.equal(body.error,"assertion_secret_not_configured");
  }
});
