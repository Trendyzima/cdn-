    assert.deepEqual(options.range, { offset: 0, length: 4 });
    return { ...makeObject("users/u1/video.mp4", "test-video", "video/mp4"), range: { offset: 0, length: 4 } };
  }}};
  const res = await worker.fetch(new Request("https://media.testagram.site/v1/users/u1/video.mp4", { headers: { Range: "bytes=0-3" }}), env);
  assert.equal(res.status, 206);
  assert.equal(res.headers.get("content-range"), "bytes 0-3/10");
  assert.equal(res.headers.get("accept-ranges"), "bytes");
});

test("path traversal never reaches R2", async () => {
  let called = false;
  const env = { MEDIA_BUCKET: { get: async () => { called = true; return null; } } };
  const res = await worker.fetch(new Request("https://media.testagram.site/v1/users/u1/../secret.webp"), env);
  assert.equal(res.status, 404);
  assert.equal(called, false);
});