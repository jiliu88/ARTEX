import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { setTimeout as delay } from 'node:timers/promises';
import { requestJSON, HttpError } from '../web/src/lib/request.ts';
import { startPolling, notePollFailure } from '../web/src/lib/polling.ts';

test('超时覆盖响应头和响应体，外部取消保留原因，HTTP 错误保留状态', async () => {
  const server = createServer((req, res) => {
    if (req.url === '/hang') return;
    if (req.url === '/body') { res.writeHead(200, {'Content-Type':'application/json'}); res.write('{'); return; }
    const code = Number(req.url.slice(1)) || 200;
    res.writeHead(code, {'Content-Type':'application/json'}); res.end(JSON.stringify({error:'synthetic error',ok:true}));
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const base = `http://127.0.0.1:${server.address().port}`;
  try {
    for (const path of ['/hang','/body']) {
      const start = Date.now();
      await assert.rejects(requestJSON(base+path,{timeoutMs:80}), e => e.name === 'TimeoutError');
      assert.ok(Date.now()-start<1000);
    }
    for (const code of [401,404,500]) await assert.rejects(requestJSON(base+'/'+code), e => e instanceof HttpError && e.status === code);
    const controller = new AbortController();
    const pending = requestJSON(base+'/hang',{signal:controller.signal});
    controller.abort(new Error('left page'));
    await assert.rejects(pending,/left page/);
    assert.equal((await requestJSON(base+'/ok')).ok,true);
    const start = Date.now();
    await assert.rejects(requestJSON(base+'/hang'), e=>e.name==='TimeoutError');
    assert.ok(Date.now()-start>=14900 && Date.now()-start<16500);
  } finally { server.closeAllConnections(); await new Promise(resolve=>server.close(resolve)); }
});

test('轮询串行、退避、后台暂停、恢复可见和离开取消', async () => {
  const doc = new EventTarget(); doc.hidden=false; globalThis.document=doc;
  const nativeTimer=globalThis.setTimeout; const delays=[];
  globalThis.setTimeout=(fn,ms,...args)=>{delays.push(ms);return nativeTimer(fn,Math.min(ms,3),...args);};
  let calls=0,active=0,max=0,aborted=0;
  const stop=startPolling(async signal=>{
    calls++;active++;max=Math.max(max,active);
    signal.addEventListener('abort',()=>aborted++);
    await delay(7);
    if(calls<=4) notePollFailure(signal);
    active--;
  },20);
  try {
    await delay(65);
    assert.equal(max,1);
    assert.ok([5000,10000,20000,30000].every(ms=>delays.includes(ms)));
    doc.hidden=true;doc.dispatchEvent(new Event('visibilitychange'));
    await delay(15);const frozen=calls;await delay(15);assert.equal(calls,frozen);
    doc.hidden=false;doc.dispatchEvent(new Event('visibilitychange'));await delay(10);assert.ok(calls>frozen);
    stop();assert.ok(aborted>0);
    const final=calls;await delay(20);assert.equal(calls,final);
  } finally {stop();globalThis.setTimeout=nativeTimer;delete globalThis.document;}
});
